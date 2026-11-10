package imagegen

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"

	"github.com/shouni/genai-kit/gemini"
)

// Request is a request for one image generation.
//
// With one reference image it is a single generation with a reference, with several it is a
// fused generation combining the reference images, and with none it is text-only generation.
// The count decides the interpretation, so single and fused generation do not get separate types.
//
// There are two ways to specify reference images, Images and References, and they cannot be
// combined. If you only pass gs://, Images is shorter to write. To send already fetched bytes, or
// to mix gs:// and bytes in a specific order, use References.
//
// gemini.GenerateOptions is embedded, so generation parameters such as SystemPrompt / AspectRatio /
// ImageSize / Seed / Temperature can be set directly through field promotion. Copying the fields
// would require keeping two places in sync every time gemini adds one, so it is embedded.
//
// Of the promoted fields, SafetySettings and PersonGeneration are filled with the following
// defaults only when unspecified.
//
//	SafetySettings   → gemini.NewSafetySettings(gemini.SafetyBlockNone)
//	PersonGeneration → gemini.PersonGenerationAllowAll
//
// The safety filter defaults to BLOCK_NONE because Vertex AI does not accept OFF, and person
// generation defaults to allowed because character generation is the main use. Explicit values
// are not overwritten; overwriting unconditionally would leave callers no way to make the safety filters stricter.
type Request struct {
	// Model is the model name used for generation. Required.
	Model string
	// Prompt is the generation instruction. It is an error if it is empty together with NegativePrompt.
	Prompt string
	// NegativePrompt is the elements that should not appear in the generation. It is not an API
	// field; it is joined onto Prompt with the separator "\n\n[Negative Prompt]\n" and sent.
	// This appearance is a compatibility contract. Downstream prompt implementations depend on this
	// rendering, so the separator string does not change.
	// It is an error if both Prompt and NegativePrompt are empty (ignoring whitespace).
	NegativePrompt string
	// Images are the gs:// URIs of the reference images. Their order affects how the model
	// interprets them, so it is preserved. Empty-string elements are not an error; they are
	// silently left out of what is sent (so that callers can express "this character has no
	// reference image" as a missing element).
	//
	// Combining it with References is ErrConflictingReferences. There is no basis for ordering the
	// two lists against each other, and order changes the result. To mix them, put everything in
	// References.
	Images []string
	// References passes the reference images as one ordered list. Each element is either a gs://
	// URI (with URI set) or bytes the caller has already fetched (with Data and MIMEType set).
	// A sequence mixing both can be expressed.
	//
	// This package does not fetch. To handle http(s) reference images, the caller decides the
	// fetch path, timeout and size limit, turns the image into bytes and then passes them.
	// If this package abstracted over the choice of fetch method, it would hand everyone
	// dependencies they do not use (an HTTP client, a cache, recompression).
	//
	// As with Images, elements with nothing to send (gemini.Attachment.IsEmpty) are not an
	// error and are silently left out. Combining Data and URI becomes ErrInvalidAttachment
	// through gemini's validation.
	References []gemini.Attachment

	gemini.GenerateOptions
}

// preparedRequest is a request that has been validated and assembled, ready to send.
type preparedRequest struct {
	model  string
	prompt string
	opts   gemini.GenerateOptions
}

// prepare validates the request and assembles it into the form for sending.
func (c *Client) prepare(req Request) (preparedRequest, error) {
	if req.Model == "" {
		return preparedRequest{}, ErrModelRequired
	}
	finalPrompt := buildFinalPrompt(req.Prompt, req.NegativePrompt)
	if finalPrompt == "" {
		return preparedRequest{}, ErrEmptyPrompt
	}

	opts := req.GenerateOptions
	// The seed is decided on the generator side exactly once, before sending. toResponse returns
	// opts.Seed as UsedSeed as is, so the value filled in here reaches the caller.
	if c.autoSeed && opts.Seed == nil {
		opts.Seed = newSeed()
	}

	return preparedRequest{
		model:  req.Model,
		prompt: finalPrompt,
		opts:   applyDefaults(opts),
	}, nil
}

// negativePromptSeparator is the separator used when joining the negative prompt onto the prompt body.
//
// This rendering is a compatibility contract. The negative prompt is not an API field, and
// downstream prompt implementations depend on the appearance of this separator, so do not change it.
const negativePromptSeparator = "\n\n[Negative Prompt]\n"

// buildFinalPrompt joins the prompt and the negative prompt.
func buildFinalPrompt(prompt, negative string) string {
	p := strings.TrimSpace(prompt)
	n := strings.TrimSpace(negative)

	if p == "" && n == "" {
		return ""
	}
	if n == "" {
		return p
	}

	return p + negativePromptSeparator + n
}

// applyDefaults fills in defaults for items the caller left unspecified.
//
// Explicitly set SafetySettings / PersonGeneration are respected. Overwriting unconditionally
// would leave callers no way to make the safety filters stricter.
func applyDefaults(opts gemini.GenerateOptions) gemini.GenerateOptions {
	if opts.SafetySettings == nil {
		// Vertex AI does not accept OFF, so BLOCK_NONE is used.
		opts.SafetySettings = gemini.NewSafetySettings(gemini.SafetyBlockNone)
	}
	// Character generation is the main use, so person generation is allowed when unspecified.
	if opts.PersonGeneration == gemini.PersonGenerationUnspecified {
		opts.PersonGeneration = gemini.PersonGenerationAllowAll
	}
	return opts
}

// referenceAttachments converts the request's reference images into attachments for sending.
//
// Images is folded into References and then validated on a single path. Writing rules
// separately per input would let details such as handling empty elements drift between them.
func referenceAttachments(req Request) ([]gemini.Attachment, error) {
	if len(req.Images) > 0 && len(req.References) > 0 {
		return nil, ErrConflictingReferences
	}

	refs := req.References
	if len(refs) == 0 {
		refs = make([]gemini.Attachment, 0, len(req.Images))
		for _, uri := range req.Images {
			refs = append(refs, gemini.Attachment{URI: uri})
		}
	}

	attachments := make([]gemini.Attachment, 0, len(refs))
	for _, ref := range refs {
		// An element with no reference has nothing to send, so drop it (see Request.Images for why).
		if ref.IsEmpty() {
			continue
		}

		// Bytes are what the caller has already fetched. The MIME type cannot be determined from bytes,
		// and a wrong declaration breaks the receiver's interpretation, so it is required rather than guessed.
		if len(ref.Data) > 0 {
			if ref.MIMEType == "" {
				return nil, ErrMissingReferenceMIMEType
			}
			attachments = append(attachments, ref)
			continue
		}

		if !isGCSURI(ref.URI) {
			return nil, fmt.Errorf("%w: %q", ErrUnsupportedReference, ref.URI)
		}
		// A URI's MIME type is optional. Attach it if the extension tells, otherwise leave it to the
		// server's detection (see mimeTypeByPath for why).
		if ref.MIMEType == "" {
			ref.MIMEType = mimeTypeByPath(ref.URI)
		}
		attachments = append(attachments, ref)
	}

	return attachments, nil
}

// newSeed returns a random seed for automatic seeding.
//
// gemini refuses values outside int32 (ErrInvalidSeed), so it stays within range.
func newSeed() *int64 {
	seed := rand.Int64N(math.MaxInt32)
	return &seed
}

// seedOrZero dereferences *int64 safely.
// nil (WithoutAutoSeed with no seed specified either) becomes 0.
func seedOrZero(seed *int64) int64 {
	if seed == nil {
		return 0
	}
	return *seed
}
