package imagegen

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/shouni/genai-kit/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gemini.Generator has a single method, so the test double can be a plain struct.
// This is the package's only injection point.
var _ gemini.Generator = (*fakeGenerator)(nil)

// generateCall is one call received by fakeGenerator.
type generateCall struct {
	Model       string
	Prompt      string
	Attachments []gemini.Attachment
	Opts        gemini.GenerateOptions
}

type fakeGenerator struct {
	mu    sync.Mutex
	calls []generateCall

	resp *gemini.Response
	err  error
}

func (f *fakeGenerator) Generate(_ context.Context, model, prompt string, attachments []gemini.Attachment, opts gemini.GenerateOptions) (*gemini.Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, generateCall{Model: model, Prompt: prompt, Attachments: attachments, Opts: opts})
	f.mu.Unlock()

	return f.resp, f.err
}

func (f *fakeGenerator) lastCall(t *testing.T) generateCall {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.calls, "the AI was never called")
	return f.calls[len(f.calls)-1]
}

// imageResponder builds a fakeGenerator that returns one inline image.
func imageResponder(mimeType string, data []byte) *fakeGenerator {
	return &fakeGenerator{resp: &gemini.Response{
		Attachments: []gemini.Attachment{{MIMEType: mimeType, Data: data}},
	}}
}

func TestNewRequiresGenerator(t *testing.T) {
	client, err := New(nil)

	assert.ErrorIs(t, err, ErrGeneratorRequired)
	assert.Nil(t, client)
}

func TestNewIgnoresNilOption(t *testing.T) {
	client, err := New(&fakeGenerator{}, nil, WithoutAutoSeed())

	require.NoError(t, err)
	assert.False(t, client.autoSeed)
}

// TestGenerateSendsPromptAndReferences checks that reference images are passed as attachments
// still as gs://, and that the response carries a record of what was sent.
func TestGenerateSendsPromptAndReferences(t *testing.T) {
	ai := imageResponder("image/png", []byte("png-bytes"))
	client, err := New(ai)
	require.NoError(t, err)

	got, err := client.Generate(context.Background(), Request{
		Model:  "imagen-test",
		Prompt: "a cat on a roof",
		Images: []string{"gs://bucket/char.png", "gs://bucket/style.jpg"},
	})
	require.NoError(t, err)

	call := ai.lastCall(t)
	assert.Equal(t, "imagen-test", call.Model)
	assert.Equal(t, "a cat on a roof", call.Prompt)
	require.Len(t, call.Attachments, 2)
	// gs:// is resolved on the model side, so no bytes are sent.
	assert.Equal(t, gemini.Attachment{URI: "gs://bucket/char.png", MIMEType: "image/png"}, call.Attachments[0])
	assert.Equal(t, gemini.Attachment{URI: "gs://bucket/style.jpg", MIMEType: "image/jpeg"}, call.Attachments[1])

	assert.Equal(t, []byte("png-bytes"), got.Data)
	assert.Equal(t, "image/png", got.MIMEType)
	assert.Equal(t, "imagen-test", got.Model)
	assert.Equal(t, "a cat on a roof", got.Prompt)
}

// TestGenerateSendsInlineReferences checks that bytes the caller already fetched reach the AI
// in their original order, interleaved with gs:// references.
//
// Fetching is not this package's job. A caller handling http(s) reference images decides the
// fetch path, timeout and size limit itself, turns the image into bytes and passes them in References.
func TestGenerateSendsInlineReferences(t *testing.T) {
	ai := imageResponder("image/png", []byte("png-bytes"))
	client, err := New(ai)
	require.NoError(t, err)

	_, err = client.Generate(context.Background(), Request{
		Model:  "imagen-test",
		Prompt: "a cat on a roof",
		References: []gemini.Attachment{
			{URI: "gs://bucket/char.png"},
			{Data: []byte("downloaded"), MIMEType: "image/webp"},
		},
	})
	require.NoError(t, err)

	call := ai.lastCall(t)
	require.Len(t, call.Attachments, 2)
	assert.Equal(t, gemini.Attachment{URI: "gs://bucket/char.png", MIMEType: "image/png"}, call.Attachments[0])
	assert.Equal(t, gemini.Attachment{Data: []byte("downloaded"), MIMEType: "image/webp"}, call.Attachments[1])
}

// TestGenerateMintsSeedBeforeSending checks that the seed is picked before sending.
//
// The API does not return the seed it picked in the response, so leaving it to the API would
// record UsedSeed as 0, making regeneration under the same conditions impossible (0 is a valid
// seed, so it cannot be told apart from "not recorded" either).
func TestGenerateMintsSeedBeforeSending(t *testing.T) {
	ai := imageResponder("image/png", []byte("x"))
	client, err := New(ai)
	require.NoError(t, err)

	got, err := client.Generate(context.Background(), Request{Model: "imagen-test", Prompt: "p"})
	require.NoError(t, err)

	sent := ai.lastCall(t).Opts.Seed
	require.NotNil(t, sent, "no seed was sent")
	assert.Equal(t, *sent, got.UsedSeed, "the sent seed and the record disagree")
	assert.GreaterOrEqual(t, got.UsedSeed, int64(0))
	// gemini refuses values outside int32, so the picked seed stays within range too.
	assert.Less(t, got.UsedSeed, int64(math.MaxInt32))
}

// TestGenerateRespectsExplicitSeed checks that a seed specified by the caller is not
// overwritten.
func TestGenerateRespectsExplicitSeed(t *testing.T) {
	seed := int64(4242)
	ai := imageResponder("image/png", []byte("x"))
	client, err := New(ai)
	require.NoError(t, err)

	got, err := client.Generate(context.Background(), Request{
		Model:           "imagen-test",
		Prompt:          "p",
		GenerateOptions: gemini.GenerateOptions{Seed: &seed},
	})
	require.NoError(t, err)

	assert.Equal(t, seed, got.UsedSeed)
	require.NotNil(t, ai.lastCall(t).Opts.Seed)
	assert.Equal(t, seed, *ai.lastCall(t).Opts.Seed)
}

// TestWithoutAutoSeedLeavesSeedToAPI checks that with automatic seeding off, no seed is sent
// and UsedSeed is 0. This setting hands seed management entirely to the caller.
func TestWithoutAutoSeedLeavesSeedToAPI(t *testing.T) {
	ai := imageResponder("image/png", []byte("x"))
	client, err := New(ai, WithoutAutoSeed())
	require.NoError(t, err)

	got, err := client.Generate(context.Background(), Request{Model: "imagen-test", Prompt: "p"})
	require.NoError(t, err)

	assert.Nil(t, ai.lastCall(t).Opts.Seed)
	assert.Zero(t, got.UsedSeed)
}

// TestGeneratePropagatesGenerationError checks that a failed generation call is returned as is.
// A block by a safety filter becomes an error on the gemini side, so swallowing it here would
// leave the caller unable to determine the reason.
func TestGeneratePropagatesGenerationError(t *testing.T) {
	sentinel := errors.New("blocked")
	client, err := New(&fakeGenerator{err: sentinel})
	require.NoError(t, err)

	_, err = client.Generate(context.Background(), Request{Model: "imagen-test", Prompt: "p"})

	assert.ErrorIs(t, err, sentinel)
}

// TestGenerateValidatesBeforeSending checks that invalid input does not call the AI.
func TestGenerateValidatesBeforeSending(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		want error
	}{
		{"empty model name", Request{Prompt: "p"}, ErrModelRequired},
		{"empty prompt", Request{Model: "m"}, ErrEmptyPrompt},
		{"whitespace-only prompt", Request{Model: "m", Prompt: "   "}, ErrEmptyPrompt},
		{"reference other than gs://", Request{Model: "m", Prompt: "p", Images: []string{"https://example.com/a.png"}}, ErrUnsupportedReference},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ai := &fakeGenerator{}
			client, err := New(ai)
			require.NoError(t, err)

			_, err = client.Generate(context.Background(), tt.req)

			assert.ErrorIs(t, err, tt.want)
			assert.Empty(t, ai.calls, "should be refused before sending")
		})
	}
}
