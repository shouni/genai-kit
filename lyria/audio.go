package lyria

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/shouni/genai-kit/callguard"
	"github.com/shouni/genai-kit/gemini"
)

// AudioGenerator generates audio from an assembled prompt.
type AudioGenerator interface {
	GenerateAudio(ctx context.Context, req AudioRequest) (*Track, error)
}

// AudioRequest is the input to one audio generation.
//
// It takes a prompt rather than a recipe because how the prompt is assembled is decided by caller policy.
// Decisions such as how to write the lyrics and which target the structure is designed for need
// information outside the recipe; if this package took a Recipe and handed it to a builder, that
// information would have to be carried on Recipe.
type AudioRequest struct {
	// Prompt is the full prompt passed to Lyria. Empty is ErrNilInput.
	Prompt string
	// Model is the audio model to use. Empty means the WithLyriaModel default.
	Model string
	// Seed is the seed passed to Lyria. nil means unspecified.
	Seed *int64
	// Images are the attached reference images.
	Images []ImagePayload
}

// Track is the result of one audio generation. The MIME type and text cannot be recreated by
// the caller, so they are returned together with the audio bytes.
type Track struct {
	// Audio is the generated audio bytes.
	Audio []byte
	// MIMEType is the MIME type of Audio (such as "audio/mpeg").
	// It is used to decide the extension and Content-Type when saving.
	MIMEType string
	// SungLyrics is the text returned together with the audio.
	//
	// lyria-3.5 returns here the score it laid out itself (measured: sections written as [[B1]] [[C2]],
	// sung lines starting with [:], lyrics in the notation they were passed in). It is not a
	// transcription of what was played, so comparing it with the requested lyrics only reveals
	// "lines the model never intended to sing in the first place".
	//
	// An empty string is not abnormal. Some models return no text, and some generations are instrumental only.
	SungLyrics string
}

// Clone returns a copy the caller can modify safely.
// Generation results are shared between concurrent calls through singleflight (callguard.Do),
// so returning without copying would let one caller's modification spill over to the other.
func (t *Track) Clone() *Track {
	if t == nil {
		return nil
	}

	dst := *t
	dst.Audio = slices.Clone(t.Audio)
	return &dst
}

// audioGenerator passes the prompt to Lyria and generates audio.
type audioGenerator struct {
	aiClient          gemini.Generator
	defaultLyriaModel string
	// A nil guard means "no limits, default time limit".
	guard *callguard.Guard
	group callguard.Group
}

// GenerateAudio turns the prompt into audio with a single Lyria call.
func (g *audioGenerator) GenerateAudio(ctx context.Context, req AudioRequest) (*Track, error) {
	if req.Prompt == "" {
		return nil, fmt.Errorf("%w: audio prompt", ErrNilInput)
	}

	targetModel := g.defaultLyriaModel
	if req.Model != "" {
		targetModel = req.Model
	}

	promptText := req.Prompt
	images := req.Images
	imageHash := imagesHash(images)
	key := callguard.Key("audio-full", targetModel, promptText, callguard.SeedKey(req.Seed), imageHash)
	track, err := callguard.Do(ctx, &g.group, g.guard, key, func(execCtx context.Context) (*Track, error) {
		resp, err := g.aiClient.Generate(
			execCtx,
			targetModel,
			promptText,
			images,
			audioOptions(req.Seed),
		)
		if err != nil {
			return nil, fmt.Errorf("lyria generation failed (model: %s): %w", targetModel, err)
		}
		audio, ok := firstAudioAttachment(resp)
		if !ok {
			return nil, fmt.Errorf("%w (model: %s)", ErrNoAudio, targetModel)
		}

		return &Track{
			Audio:      audio.Data,
			MIMEType:   audio.MIMEType,
			SungLyrics: resp.Text,
		}, nil
	})
	if err != nil {
		return nil, err
	}

	return track.Clone(), nil
}

// firstAudioAttachment extracts the first audio attachment from the response, along with its MIME type.
//
// It looks at Attachments rather than Response.Audios to keep the MIME type. Audios is a
// subset of Attachments filtered by the same prefix check, so the selected element does not change.
func firstAudioAttachment(resp *gemini.Response) (gemini.Attachment, bool) {
	if resp == nil {
		return gemini.Attachment{}, false
	}
	for _, attachment := range resp.Attachments {
		if strings.HasPrefix(attachment.MIMEType, "audio/") {
			return attachment, true
		}
	}

	return gemini.Attachment{}, false
}

// imagesHash builds a singleflight key component from the content of the image payloads.
//
// The bytes are streamed as is with a length prefix (the same framing as callguard.WriteHashPart)
// because converting them to a string to pass to callguard.Key would copy several MB on every key build.
func imagesHash(images []ImagePayload) string {
	hasher := sha256.New()
	for _, image := range images {
		if len(image.Data) == 0 {
			continue
		}

		callguard.WriteHashPart(hasher, []byte(image.MIMEType))
		callguard.WriteHashPart(hasher, image.Data)
	}

	return "images:" + hex.EncodeToString(hasher.Sum(nil))
}
