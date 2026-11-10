package lyria

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/shouni/genai-kit/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This package's test doubles are hand-written. Every injected interface has only one or two
// methods, so plain structs are shorter than going through a mocking library. With a mechanism
// that names methods by string, a rename on the gemini side would also surface at run time
// (as an expectation mismatch) rather than at compile time, which is a real cost.
var (
	_ gemini.Generator  = (*fakeGenerator)(nil)
	_ TextPromptBuilder = (*stubTextPrompts)(nil)
)

// generateCall is one call received by fakeGenerator.
type generateCall struct {
	Model       string
	Prompt      string
	Attachments []gemini.Attachment
	Opts        gemini.GenerateOptions
}

// fakeGenerator is a test double for gemini.Generator.
type fakeGenerator struct {
	mu    sync.Mutex
	calls []generateCall

	// If respond is non-nil it is used; otherwise resp / err are returned as is.
	respond func(call generateCall) (*gemini.Response, error)
	resp    *gemini.Response
	err     error

	// If block is non-nil, it waits for this channel to close before responding.
	// Used to make callers join in singleflight.
	block chan struct{}
}

func (f *fakeGenerator) Generate(_ context.Context, model, prompt string, attachments []gemini.Attachment, opts gemini.GenerateOptions) (*gemini.Response, error) {
	call := generateCall{Model: model, Prompt: prompt, Attachments: attachments, Opts: opts}

	f.mu.Lock()
	f.calls = append(f.calls, call)
	respond, resp, err, block := f.respond, f.resp, f.err, f.block
	f.mu.Unlock()

	if block != nil {
		<-block
	}
	if respond != nil {
		return respond(call)
	}
	return resp, err
}

func (f *fakeGenerator) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// lastCall returns the most recent call. If it was never called, the test fails.
func (f *fakeGenerator) lastCall(t *testing.T) generateCall {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.calls, "the AI was never called")
	return f.calls[len(f.calls)-1]
}

// textResponder builds a fakeGenerator that returns a text generation response.
func textResponder(text string) *fakeGenerator {
	return &fakeGenerator{resp: &gemini.Response{Text: text}}
}

// audioResponder builds a fakeGenerator that returns audio.
func audioResponder(audio []byte) *fakeGenerator {
	return &fakeGenerator{resp: audioResponse("audio/mpeg", audio, "")}
}

// audioResponse builds a Lyria audio response for tests.
//
// Both Attachments and Audios are filled because the gemini-side conversion builds them that way. A
// response with only Audios never actually comes back, so a fake imitating one could not test the MIME type path.
func audioResponse(mimeType string, audio []byte, text string) *gemini.Response {
	return &gemini.Response{
		Text:        text,
		Audios:      [][]byte{audio},
		Attachments: []gemini.Attachment{{MIMEType: mimeType, Data: audio}},
	}
}

// stubTextPrompts is a test double for TextPromptBuilder.
// It records the mode and input it received and returns a fixed prompt (or error).
//
// The record is guarded by a mutex because the deduplication test calls the same stub from several
// goroutines (reading the record happens after they join, so a plain read is fine).
type stubTextPrompts struct {
	lyricsPrompt string
	recipePrompt string
	lyricsErr    error
	recipeErr    error

	mu             sync.Mutex
	gotLyricsMode  string
	gotLyricsInput string
	gotRecipeMode  string
	gotRecipeDraft *LyricsDraft
}

func (s *stubTextPrompts) LyricsPrompt(mode string, input string) (string, error) {
	s.mu.Lock()
	s.gotLyricsMode, s.gotLyricsInput = mode, input
	s.mu.Unlock()

	return s.lyricsPrompt, s.lyricsErr
}

func (s *stubTextPrompts) RecipePrompt(mode string, lyrics *LyricsDraft) (string, error) {
	s.mu.Lock()
	s.gotRecipeMode, s.gotRecipeDraft = mode, lyrics
	s.mu.Unlock()

	return s.recipePrompt, s.recipeErr
}

const validLyricsJSON = `{"title":"t","theme":"th","hook":"h","lyrics":"l"}`

// newTestWorkflow builds a minimal Workflow with the model names filled in.
// The firing interval defaults to 0 (no limit).
func newTestWorkflow(t *testing.T, ai gemini.Generator, text TextPromptBuilder, extra ...Option) *Workflow {
	t.Helper()

	base := []Option{WithGeminiModel("gemini-flash"), WithLyriaModel("lyria-3")}
	w, err := New(ai, text, append(base, extra...)...)
	require.NoError(t, err)
	return w
}

// TestNewRequiresDependenciesAndModels checks that missing dependencies and model names are refused
// at construction. Model names have no defaults because the choice directly determines quality and cost.
func TestNewRequiresDependenciesAndModels(t *testing.T) {
	ai := &fakeGenerator{}
	text := &stubTextPrompts{}

	tests := []struct {
		name string
		ai   gemini.Generator
		text TextPromptBuilder
		opts []Option
	}{
		{"aiClient is nil", nil, text, []Option{WithGeminiModel("g"), WithLyriaModel("l")}},
		{"textPrompts is nil", ai, nil, []Option{WithGeminiModel("g"), WithLyriaModel("l")}},
		{"GeminiModel is unspecified", ai, text, []Option{WithLyriaModel("l")}},
		{"LyriaModel is unspecified", ai, text, []Option{WithGeminiModel("g")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := New(tt.ai, tt.text, tt.opts...)

			require.ErrorIs(t, err, ErrWorkflowConfig)
			assert.Nil(t, w)
		})
	}

	t.Run("succeeds when everything is present", func(t *testing.T) {
		w, err := New(ai, text, WithGeminiModel("g"), WithLyriaModel("l"))

		require.NoError(t, err)
		require.NotNil(t, w)
	})
}

// TestNewIgnoresNilOption checks that passing a nil Option does not crash.
// Callers that build options conditionally need not write branches.
func TestNewIgnoresNilOption(t *testing.T) {
	_, err := New(&fakeGenerator{}, &stubTextPrompts{},
		WithGeminiModel("g"), nil, WithLyriaModel("l"))

	require.NoError(t, err)
}

// TestWorkflowDelegatesToEachRole checks that the facade's three methods delegate to their
// respective roles, with text going to the Gemini model and audio to the Lyria model.
func TestWorkflowDelegatesToEachRole(t *testing.T) {
	ctx := context.Background()

	t.Run("lyrics generation goes to the Gemini model", func(t *testing.T) {
		ai := textResponder(validLyricsJSON)
		w := newTestWorkflow(t, ai, &stubTextPrompts{lyricsPrompt: "lyrics prompt"})

		draft, err := w.GenerateLyrics(ctx, AIModels{}, &CollectedContent{Prompt: "input"})

		require.NoError(t, err)
		assert.Equal(t, "l", draft.Lyrics)
		assert.Equal(t, "gemini-flash", ai.lastCall(t).Model)
	})

	t.Run("composition goes to the Gemini model", func(t *testing.T) {
		ai := textResponder(`{"title":"Song","tempo":120}`)
		w := newTestWorkflow(t, ai, &stubTextPrompts{recipePrompt: "recipe prompt"})

		recipe, err := w.Compose(ctx, AIModels{}, &LyricsDraft{Lyrics: "l"})

		require.NoError(t, err)
		assert.Equal(t, "Song", recipe.Title)
		assert.Equal(t, "gemini-flash", ai.lastCall(t).Model)
	})

	t.Run("audio generation goes to the Lyria model", func(t *testing.T) {
		ai := audioResponder([]byte{1, 2, 3})
		w := newTestWorkflow(t, ai, &stubTextPrompts{})

		track, err := w.GenerateAudio(ctx, AudioRequest{Prompt: "full prompt"})

		require.NoError(t, err)
		assert.Equal(t, []byte{1, 2, 3}, track.Audio)

		call := ai.lastCall(t)
		assert.Equal(t, "lyria-3", call.Model)
		assert.Equal(t, "full prompt", call.Prompt)
	})
}

// TestNewRoutesAudioToAudioGenerator checks that passing WithAudioGenerator sends only audio
// generation there, while lyrics and composition still go to aiClient.
//
// This separation keeps the text side out of it while audio is diverted to a direct REST implementation.
// It also checks that when unspecified both go back to aiClient (removing the replacement restores the original).
func TestNewRoutesAudioToAudioGenerator(t *testing.T) {
	ctx := context.Background()

	t.Run("only audio goes there when specified", func(t *testing.T) {
		text := textResponder(validLyricsJSON)
		audioAI := audioResponder([]byte{1, 2, 3})
		w := newTestWorkflow(t, text, &stubTextPrompts{lyricsPrompt: "p"},
			WithAudioGenerator(audioAI))

		_, err := w.GenerateLyrics(ctx, AIModels{}, &CollectedContent{Prompt: "x"})
		require.NoError(t, err)
		track, err := w.GenerateAudio(ctx, AudioRequest{Prompt: "full prompt"})
		require.NoError(t, err)

		assert.Equal(t, 1, text.callCount(), "lyrics go to aiClient")
		assert.Equal(t, 1, audioAI.callCount(), "audio goes to WithAudioGenerator")
		assert.Equal(t, "lyria-3", audioAI.lastCall(t).Model)
		assert.Equal(t, []byte{1, 2, 3}, track.Audio)
	})

	t.Run("both go to aiClient when unspecified", func(t *testing.T) {
		ai := &fakeGenerator{respond: func(call generateCall) (*gemini.Response, error) {
			if call.Model == "lyria-3" {
				return audioResponse("audio/mpeg", []byte{1}, ""), nil
			}
			return &gemini.Response{Text: validLyricsJSON}, nil
		}}
		w := newTestWorkflow(t, ai, &stubTextPrompts{lyricsPrompt: "p"})

		_, err := w.GenerateLyrics(ctx, AIModels{}, &CollectedContent{Prompt: "x"})
		require.NoError(t, err)
		_, err = w.GenerateAudio(ctx, AudioRequest{Prompt: "full prompt"})
		require.NoError(t, err)

		assert.Equal(t, 2, ai.callCount())
	})
}

// TestTextAndAudioHaveSeparateRateGuards checks that the firing intervals for text and audio are
// independent. They are different quotas of different models, so there is no reason to throttle
// one when the other is busy. If they were shared, the side that need not wait would be kept waiting too.
func TestTextAndAudioHaveSeparateRateGuards(t *testing.T) {
	const textInterval = 50 * time.Millisecond
	const audioInterval = 30 * time.Second

	t.Run("the text firing interval does not affect audio", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ai := &fakeGenerator{respond: func(call generateCall) (*gemini.Response, error) {
				if call.Model == "lyria-3" {
					return audioResponse("audio/mpeg", []byte{1}, ""), nil
				}
				return &gemini.Response{Text: validLyricsJSON}, nil
			}}
			w := newTestWorkflow(t, ai, &stubTextPrompts{lyricsPrompt: "p"},
				WithTextRateInterval(textInterval), WithRateInterval(0))

			ctx := context.Background()
			start := time.Now()
			_, err := w.GenerateLyrics(ctx, AIModels{}, &CollectedContent{Prompt: "first"})
			require.NoError(t, err)
			_, err = w.GenerateLyrics(ctx, AIModels{}, &CollectedContent{Prompt: "second"})
			require.NoError(t, err)

			// With the virtual clock, the time waited exactly matches the configured value.
			require.Equal(t, textInterval, time.Since(start), "the second text generation should wait one firing interval")

			// Audio has no limit, so it is not dragged down by congestion on the text side.
			beforeAudio := time.Now()
			_, err = w.GenerateAudio(ctx, AudioRequest{Prompt: "full prompt"})
			require.NoError(t, err)
			assert.Zero(t, time.Since(beforeAudio), "audio generation is caught up in the text firing interval")
		})
	})

	t.Run("the audio firing interval does not affect text", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ai := &fakeGenerator{respond: func(call generateCall) (*gemini.Response, error) {
				if call.Model == "lyria-3" {
					return audioResponse("audio/mpeg", []byte{1}, ""), nil
				}
				return &gemini.Response{Text: validLyricsJSON}, nil
			}}
			w := newTestWorkflow(t, ai, &stubTextPrompts{lyricsPrompt: "p"},
				WithRateInterval(audioInterval), WithTextRateInterval(0))

			ctx := context.Background()
			// Use up one slot on the audio side.
			_, err := w.GenerateAudio(ctx, AudioRequest{Prompt: "full prompt"})
			require.NoError(t, err)

			start := time.Now()
			_, err = w.GenerateLyrics(ctx, AIModels{}, &CollectedContent{Prompt: "first"})
			require.NoError(t, err)
			assert.Zero(t, time.Since(start), "text generation is caught up in the audio firing interval")
		})
	})
}

// TestWorkflowPropagatesRoleErrors checks that each stage's failure passes straight through the
// facade to the caller. It is the caller that places quality gates between stages, so swallowing
// the failure here would remove what it bases its decision on.
func TestWorkflowPropagatesRoleErrors(t *testing.T) {
	ctx := context.Background()
	sentinel := errors.New("ai boom")
	ai := &fakeGenerator{err: sentinel}
	w := newTestWorkflow(t, ai, &stubTextPrompts{lyricsPrompt: "p", recipePrompt: "p"})

	_, err := w.GenerateLyrics(ctx, AIModels{}, &CollectedContent{Prompt: "x"})
	assert.ErrorIs(t, err, sentinel)

	_, err = w.Compose(ctx, AIModels{}, &LyricsDraft{Lyrics: "l"})
	assert.ErrorIs(t, err, sentinel)

	_, err = w.GenerateAudio(ctx, AudioRequest{Prompt: "full prompt"})
	assert.ErrorIs(t, err, sentinel)
}
