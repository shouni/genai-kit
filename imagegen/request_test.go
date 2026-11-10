package imagegen

import (
	"math"
	"strings"
	"testing"

	"github.com/shouni/genai-kit/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildFinalPromptRendersNegativePrompt checks how the negative prompt is rendered.
//
// This is a compatibility contract. The negative prompt is not an API field, and downstream
// prompt implementations depend on the exact appearance of this separator.
func TestBuildFinalPromptRendersNegativePrompt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		prompt   string
		negative string
		want     string
	}{
		{"both empty", "", "", ""},
		{"body only", "a cat", "", "a cat"},
		{"negative only", "", "blurry", "\n\n[Negative Prompt]\nblurry"},
		{"both", "a cat", "blurry", "a cat\n\n[Negative Prompt]\nblurry"},
		{"trims surrounding whitespace", "  a cat  ", "  blurry  ", "a cat\n\n[Negative Prompt]\nblurry"},
		{"whitespace only counts as empty", "   ", "   ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, buildFinalPrompt(tt.prompt, tt.negative))
		})
	}
}

// TestNegativePromptSeparatorIsStable pins the exact appearance of the separator.
// Changing it breaks matching in downstream prompt implementations.
func TestNegativePromptSeparatorIsStable(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "\n\n[Negative Prompt]\n", negativePromptSeparator)
}

// TestApplyDefaultsFillsOnlyUnsetFields checks that explicitly set safety settings and person
// generation are not overwritten. Overwriting unconditionally would leave callers no way to make
// the safety filters stricter.
func TestApplyDefaultsFillsOnlyUnsetFields(t *testing.T) {
	t.Parallel()

	t.Run("fills in defaults when unset", func(t *testing.T) {
		got := applyDefaults(gemini.GenerateOptions{})

		require.NotEmpty(t, got.SafetySettings)
		for _, setting := range got.SafetySettings {
			// Vertex AI does not accept OFF, so BLOCK_NONE is used.
			assert.Equal(t, gemini.SafetyBlockNone, setting.Threshold)
		}
		// Character generation is the main use, so person generation is allowed when unspecified.
		assert.Equal(t, gemini.PersonGenerationAllowAll, got.PersonGeneration)
	})

	t.Run("respects explicitly set values", func(t *testing.T) {
		strict := gemini.NewSafetySettings(gemini.SafetyBlockLowAndAbove)

		got := applyDefaults(gemini.GenerateOptions{
			SafetySettings:   strict,
			PersonGeneration: gemini.PersonGenerationDontAllow,
		})

		assert.Equal(t, strict, got.SafetySettings)
		assert.Equal(t, gemini.PersonGenerationDontAllow, got.PersonGeneration)
	})
}

// TestPrepareCombinesPromptAndDefaults checks that validation, prompt joining and filling in
// defaults are done in one pass.
func TestPrepareCombinesPromptAndDefaults(t *testing.T) {
	t.Parallel()

	client := &Client{ai: &fakeGenerator{}, autoSeed: false}

	got, err := client.prepare(Request{
		Model:          "imagen-test",
		Prompt:         "a cat",
		NegativePrompt: "blurry",
	})
	require.NoError(t, err)

	assert.Equal(t, "imagen-test", got.model)
	assert.Equal(t, "a cat"+negativePromptSeparator+"blurry", got.prompt)
	assert.NotEmpty(t, got.opts.SafetySettings)
	assert.Nil(t, got.opts.Seed, "automatic seeding is off, so no seed is attached")
}

// TestPrepareAcceptsNegativePromptOnly checks that a negative prompt alone can be sent.
// If the joined string is not empty, it is a valid instruction.
func TestPrepareAcceptsNegativePromptOnly(t *testing.T) {
	t.Parallel()

	client := &Client{ai: &fakeGenerator{}}

	got, err := client.prepare(Request{Model: "m", NegativePrompt: "blurry"})

	require.NoError(t, err)
	assert.Contains(t, got.prompt, "blurry")
}

// TestReferenceAttachmentsKeepsOrderAndDropsEmpty checks that the order of reference images
// is kept and that empty-string elements are silently dropped.
//
// Empty elements are dropped so that callers can express "this character has no reference
// image" as a missing element.
func TestReferenceAttachmentsKeepsOrderAndDropsEmpty(t *testing.T) {
	t.Parallel()

	got, err := referenceAttachments(Request{Images: []string{
		"gs://bucket/a.png",
		"",
		"gs://bucket/b.webp",
	}})
	require.NoError(t, err)

	require.Len(t, got, 2)
	assert.Equal(t, "gs://bucket/a.png", got[0].URI)
	assert.Equal(t, "gs://bucket/b.webp", got[1].URI)
}

// TestReferenceAttachmentsHintsMIMETypeFromExtension checks that the MIME type is guessed from
// the extension, and not declared when it cannot be determined. A wrong declaration breaks the
// receiver's interpretation, so the decision is left to the server.
func TestReferenceAttachmentsHintsMIMETypeFromExtension(t *testing.T) {
	t.Parallel()

	got, err := referenceAttachments(Request{Images: []string{
		"gs://bucket/a.png",
		"gs://bucket/b.JPEG",
		"gs://bucket/c",
	}})
	require.NoError(t, err)

	require.Len(t, got, 3)
	assert.Equal(t, "image/png", got[0].MIMEType)
	assert.Equal(t, "image/jpeg", got[1].MIMEType)
	assert.Empty(t, got[2].MIMEType, "an unrecognised extension is not declared")
}

func TestReferenceAttachmentsRejectsNonGCSURIs(t *testing.T) {
	t.Parallel()

	for _, uri := range []string{"https://example.com/a.png", "file:///tmp/a.png", "a.png"} {
		_, err := referenceAttachments(Request{Images: []string{uri}})

		assert.ErrorIs(t, err, ErrUnsupportedReference, "uri = %q", uri)
	}
}

// TestReferenceAttachmentsAcceptsInlineBytes checks that bytes the caller already fetched can be
// passed interleaved with gs:// in their original order.
//
// Order changes the result, so splitting gs:// and bytes into separate lists could not express
// a mixed sequence. That is why References is a single list.
func TestReferenceAttachmentsAcceptsInlineBytes(t *testing.T) {
	t.Parallel()

	got, err := referenceAttachments(Request{References: []gemini.Attachment{
		{URI: "gs://bucket/a.png"},
		{Data: []byte("fetched"), MIMEType: "image/webp"},
		{},
		{URI: "gs://bucket/b.png"},
	}})
	require.NoError(t, err)

	require.Len(t, got, 3, "elements with nothing to send are silently dropped")
	assert.Equal(t, "gs://bucket/a.png", got[0].URI)
	assert.Equal(t, []byte("fetched"), got[1].Data)
	assert.Equal(t, "image/webp", got[1].MIMEType)
	assert.Equal(t, "gs://bucket/b.png", got[2].URI)
}

// TestReferenceAttachmentsRequiresMIMETypeForInlineBytes checks that bytes need a MIME type.
// The type cannot be determined from the bytes, and a wrong declaration breaks the receiver's
// interpretation, so it is required rather than guessed.
func TestReferenceAttachmentsRequiresMIMETypeForInlineBytes(t *testing.T) {
	t.Parallel()

	_, err := referenceAttachments(Request{References: []gemini.Attachment{
		{Data: []byte("fetched")},
	}})

	assert.ErrorIs(t, err, ErrMissingReferenceMIMEType)
}

// TestReferenceAttachmentsRejectsBothInputs checks that using Images and References together
// is refused. There is no basis for ordering the two lists against each other, and silently
// joining them would send them in an order the caller did not intend.
func TestReferenceAttachmentsRejectsBothInputs(t *testing.T) {
	t.Parallel()

	_, err := referenceAttachments(Request{
		Images:     []string{"gs://bucket/a.png"},
		References: []gemini.Attachment{{URI: "gs://bucket/b.png"}},
	})

	assert.ErrorIs(t, err, ErrConflictingReferences)
}

// TestNewSeedStaysWithinInt32 checks that a picked seed stays within the range gemini accepts.
// Values outside it are refused with ErrInvalidSeed.
func TestNewSeedStaysWithinInt32(t *testing.T) {
	t.Parallel()

	for range 100 {
		got := newSeed()

		require.NotNil(t, got)
		assert.GreaterOrEqual(t, *got, int64(0))
		assert.Less(t, *got, int64(math.MaxInt32))
	}
}

func TestSeedOrZero(t *testing.T) {
	t.Parallel()

	seed := int64(42)

	assert.Equal(t, int64(42), seedOrZero(&seed))
	assert.Zero(t, seedOrZero(nil))
}

// TestRequestPromotesGenerateOptions checks that fields can be set directly through promotion
// by embedding. It embeds rather than copies so that two places need not be kept in sync every
// time gemini adds a field.
func TestRequestPromotesGenerateOptions(t *testing.T) {
	t.Parallel()

	req := Request{Model: "m", Prompt: "p"}
	req.SystemPrompt = "system"
	req.AspectRatio = "16:9"
	req.Temperature = new(float32(0))

	// Assignments to the promoted fields reach the embedded value itself.
	opts := req.GenerateOptions
	assert.Equal(t, "system", opts.SystemPrompt)
	assert.Equal(t, "16:9", opts.AspectRatio)
	require.NotNil(t, opts.Temperature)
	assert.Zero(t, *opts.Temperature)
}

// TestPrepareForwardsPromotedOptions checks that the promoted fields are carried into the
// sending options as is.
func TestPrepareForwardsPromotedOptions(t *testing.T) {
	t.Parallel()

	client := &Client{ai: &fakeGenerator{}}
	req := Request{Model: "m", Prompt: strings.Repeat("p", 3)}
	req.AspectRatio = "9:16"
	req.ImageSize = "2K"

	got, err := client.prepare(req)

	require.NoError(t, err)
	assert.Equal(t, "9:16", got.opts.AspectRatio)
	assert.Equal(t, "2K", got.opts.ImageSize)
}
