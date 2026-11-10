package imagegen

import (
	"testing"

	"github.com/shouni/genai-kit/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPrepared(seed *int64) preparedRequest {
	return preparedRequest{
		model:  "imagen-test",
		prompt: "a cat",
		opts:   gemini.GenerateOptions{Seed: seed},
	}
}

// TestToResponseTakesFirstInlineImage checks that the first inline image is extracted along
// with its MIME type.
//
// gemini.Response.Attachments come back with their MIME type, so there is no need to walk the
// raw SDK response to decide the Content-Type when saving.
func TestToResponseTakesFirstInlineImage(t *testing.T) {
	seed := int64(42)
	usage := &gemini.TokenUsage{PromptTokenCount: 10, TotalTokenCount: 12}

	got, err := newPrepared(&seed).toResponse(&gemini.Response{
		Attachments: []gemini.Attachment{
			{MIMEType: "image/png"}, // an attachment with no data is skipped
			{MIMEType: "image/webp", Data: []byte("webp-bytes")},
			{MIMEType: "image/png", Data: []byte("png-bytes")},
		},
		Usage: usage,
	})
	require.NoError(t, err)

	assert.Equal(t, []byte("webp-bytes"), got.Data)
	assert.Equal(t, "image/webp", got.MIMEType)
	assert.Equal(t, int64(42), got.UsedSeed)
	assert.Equal(t, "imagen-test", got.Model)
	assert.Equal(t, "a cat", got.Prompt)
	assert.Same(t, usage, got.Usage)
}

// TestToResponseTakesOutputURI checks that an image written to GCS because OutputURIPrefix was
// specified is returned as a URI. When the server did not attach a MIME type, it is filled in
// from the destination's extension.
func TestToResponseTakesOutputURI(t *testing.T) {
	tests := []struct {
		name     string
		in       gemini.Attachment
		wantMIME string
	}{
		{"uses the server's MIME type as is", gemini.Attachment{URI: "gs://b/out/1", MIMEType: "image/webp"}, "image/webp"},
		{"falls back to the extension when absent", gemini.Attachment{URI: "gs://b/out/1.png"}, "image/png"},
		{"stays empty when the extension does not tell either", gemini.Attachment{URI: "gs://b/out/1"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newPrepared(nil).toResponse(&gemini.Response{
				Attachments: []gemini.Attachment{tt.in},
			})
			require.NoError(t, err)

			assert.Empty(t, got.Data)
			assert.Equal(t, tt.in.URI, got.URI)
			assert.Equal(t, tt.wantMIME, got.MIMEType)
		})
	}
}

// TestToResponseReportsMissingImage checks that a response with no image becomes a
// distinguishable error. A block by a safety filter becomes an error on the gemini side,
// so the only thing this package distinguishes is "no image data".
func TestToResponseReportsMissingImage(t *testing.T) {
	tests := []struct {
		name string
		resp *gemini.Response
	}{
		{"response is nil", nil},
		{"no attachments", &gemini.Response{Text: "description only"}},
		{"attachment present but data empty", &gemini.Response{Attachments: []gemini.Attachment{{MIMEType: "image/png"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newPrepared(nil).toResponse(tt.resp)

			assert.ErrorIs(t, err, ErrNoImageData)
		})
	}
}

// TestToResponseRecordsZeroSeedWhenUnset checks that UsedSeed is 0 when no seed is set
// (WithoutAutoSeed). Automatic seeding is the default because this 0 cannot be told apart
// from "a seed the API picked".
func TestToResponseRecordsZeroSeedWhenUnset(t *testing.T) {
	got, err := newPrepared(nil).toResponse(&gemini.Response{
		Attachments: []gemini.Attachment{{MIMEType: "image/png", Data: []byte("x")}},
	})
	require.NoError(t, err)

	assert.Zero(t, got.UsedSeed)
}
