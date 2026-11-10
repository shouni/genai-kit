package lyria

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/shouni/genai-kit/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTextGenerator builds a textGenerator with no firing interval.
// guard can be left nil because callguard treats nil as "no limits, default time
// limit".
func newTextGenerator(ai gemini.Generator, prompts TextPromptBuilder) *textGenerator {
	return &textGenerator{aiClient: ai, prompts: prompts, defaultModel: "gemini-flash"}
}

// TestGenerateLyricsSendsJSONOptions checks that lyrics generation is sent with the structured-output
// settings. If the schema is dropped, the model output is no longer grammatically constrained, and
// broken JSON has to rely on CleanJSONResponse.
func TestGenerateLyricsSendsJSONOptions(t *testing.T) {
	seed := int64(42)
	ai := textResponder(validLyricsJSON)
	prompts := &stubTextPrompts{lyricsPrompt: "lyrics prompt"}
	g := newTextGenerator(ai, prompts)

	_, err := g.GenerateLyrics(context.Background(), AIModels{LyricsMode: "ballad", Seed: &seed},
		&CollectedContent{Prompt: "collected text"})
	require.NoError(t, err)

	// The mode and input are passed to prompt construction as is.
	assert.Equal(t, "ballad", prompts.gotLyricsMode)
	assert.Equal(t, "collected text", prompts.gotLyricsInput)

	call := ai.lastCall(t)
	assert.Equal(t, "lyrics prompt", call.Prompt)
	assert.Equal(t, "application/json", call.Opts.ResponseMIMEType)
	assert.NotNil(t, call.Opts.ResponseSchema, "no structured-output schema was specified")
	assert.NotEmpty(t, call.Opts.SafetySettings, "no safety settings were specified")
	if assert.NotNil(t, call.Opts.Seed) {
		assert.Equal(t, seed, *call.Opts.Seed)
	}
	assert.Nil(t, call.Attachments, "no attachments are sent for lyrics generation")
}

// TestGenerateLyricsResolvesModel checks that a per-call model takes precedence over the
// default.
func TestGenerateLyricsResolvesModel(t *testing.T) {
	tests := []struct {
		name      string
		textModel string
		want      string
	}{
		{"default model when unspecified", "", "gemini-flash"},
		{"uses the specified model when given", "gemini-pro", "gemini-pro"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ai := textResponder(validLyricsJSON)
			g := newTextGenerator(ai, &stubTextPrompts{lyricsPrompt: "p"})

			_, err := g.GenerateLyrics(context.Background(), AIModels{TextModel: tt.textModel},
				&CollectedContent{Prompt: "x"})

			require.NoError(t, err)
			assert.Equal(t, tt.want, ai.lastCall(t).Model)
		})
	}
}

// TestGenerateLyricsCleansJSONResponse checks that a response wrapped in a Markdown fence can
// still be parsed. This breakage happens even with structured output specified.
func TestGenerateLyricsCleansJSONResponse(t *testing.T) {
	ai := textResponder("```json\n" + validLyricsJSON + "\n```")
	g := newTextGenerator(ai, &stubTextPrompts{lyricsPrompt: "p"})

	draft, err := g.GenerateLyrics(context.Background(), AIModels{}, &CollectedContent{Prompt: "x"})

	require.NoError(t, err)
	assert.Equal(t, "t", draft.Title)
	assert.Equal(t, "l", draft.Lyrics)
}

func TestGenerateLyricsErrorBranches(t *testing.T) {
	tests := []struct {
		name        string
		prompts     *stubTextPrompts
		ai          *fakeGenerator
		wantErr     error
		wantAlsoErr error
		wantMessage string
	}{
		{
			name:        "prompt construction fails",
			prompts:     &stubTextPrompts{lyricsErr: errors.New("prompt boom")},
			ai:          &fakeGenerator{},
			wantMessage: "failed to build lyrics prompt",
		},
		{
			name:        "AI returns an error",
			prompts:     &stubTextPrompts{lyricsPrompt: "p"},
			ai:          &fakeGenerator{err: errors.New("ai boom")},
			wantMessage: "lyrics generation failed",
		},
		{
			name:        "AI returns a nil response",
			prompts:     &stubTextPrompts{lyricsPrompt: "p"},
			ai:          &fakeGenerator{},
			wantErr:     ErrInvalidResponse,
			wantMessage: "lyrics response is nil",
		},
		{
			name:        "AI returns an empty string",
			prompts:     &stubTextPrompts{lyricsPrompt: "p"},
			ai:          textResponder("   "),
			wantErr:     ErrInvalidResponse,
			wantAlsoErr: gemini.ErrEmptyResponse,
		},
		{
			name:        "cannot be parsed as JSON",
			prompts:     &stubTextPrompts{lyricsPrompt: "p"},
			ai:          textResponder("this is not JSON"),
			wantErr:     ErrInvalidResponse,
			wantAlsoErr: gemini.ErrInvalidJSON,
			wantMessage: "this is not JSON", // the excerpt remains
		},
		{
			// Satisfies the schema but the body is empty. A failure that retrying is unlikely to fix.
			name:    "passes the schema but the lyrics are empty",
			prompts: &stubTextPrompts{lyricsPrompt: "p"},
			ai:      textResponder(`{"title":"t","theme":"th","hook":"h","lyrics":"  "}`),
			wantErr: ErrEmptyLyrics,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newTextGenerator(tt.ai, tt.prompts)

			_, err := g.GenerateLyrics(context.Background(), AIModels{}, &CollectedContent{Prompt: "x"})

			require.Error(t, err)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			}
			if tt.wantAlsoErr != nil {
				assert.ErrorIs(t, err, tt.wantAlsoErr, "the gemini-side sentinel stays in the chain too")
			}
			if tt.wantMessage != "" {
				assert.Contains(t, err.Error(), tt.wantMessage)
			}
		})
	}
}

func TestGenerateLyricsRejectsNilInput(t *testing.T) {
	g := newTextGenerator(&fakeGenerator{}, &stubTextPrompts{})

	_, err := g.GenerateLyrics(context.Background(), AIModels{}, nil)

	assert.ErrorIs(t, err, ErrNilInput)
}

// TestComposeAttachesModelsAndSeed checks that the AIModels and Seed from the call are attached
// to the generated recipe.
//
// The recipe schema deliberately leaves these out (the model does not generate them), and code
// attaches them after generation. If this were missing, reproducible generation with a specified
// Seed would stop working with neither an error nor a warning.
func TestComposeAttachesModelsAndSeed(t *testing.T) {
	seed := int64(42)
	ai := textResponder(`{"title":"Rainy Amsterdam","tempo":85,"mood":"melancholic"}`)
	prompts := &stubTextPrompts{recipePrompt: "recipe prompt"}
	g := newTextGenerator(ai, prompts)

	lyrics := &LyricsDraft{Title: "Rainy Amsterdam", Lyrics: "Canals reflect the neon lights..."}
	models := AIModels{
		TextModel:   "custom-text-model",
		AudioModel:  "lyria-custom-v1",
		ComposeMode: "jazz",
		Seed:        &seed,
	}

	recipe, err := g.Compose(context.Background(), models, lyrics)
	require.NoError(t, err)

	assert.Equal(t, "jazz", prompts.gotRecipeMode)
	assert.Equal(t, "Rainy Amsterdam", recipe.Title)
	assert.Equal(t, "custom-text-model", recipe.TextModel)
	assert.Equal(t, "lyria-custom-v1", recipe.AudioModel)
	if assert.NotNil(t, recipe.Seed) {
		assert.Equal(t, seed, *recipe.Seed)
	}
	// They are attached to a copy, not the shared result (the Seed pointer is not reused).
	assert.NotSame(t, models.Seed, recipe.Seed)

	// The lyrics are also attached after generation, and are a copy too.
	require.NotNil(t, recipe.Lyrics)
	assert.Equal(t, "Rainy Amsterdam", recipe.Lyrics.Title)
	assert.NotSame(t, lyrics, recipe.Lyrics)
}

// TestComposeParsesFullRecipe checks that each field of the recipe JSON is picked up.
// The sections' timeline is validated when the recipe is reused, so if it were dropped
// it would be refused downstream.
func TestComposeParsesFullRecipe(t *testing.T) {
	const rawJSON = `{
		"title": "Lofi Chill",
		"tempo": 70,
		"mood": "relaxed",
		"key": "D minor",
		"vocal_profile": "Japanese female vocal, clear diction",
		"instruments": ["synth"],
		"sections": [
			{"name":"Verse","duration_seconds":30,"start_seconds":0,"end_seconds":30,"prompt":"soft opening groove"}
		]
	}`
	g := newTextGenerator(textResponder(rawJSON), &stubTextPrompts{recipePrompt: "p"})

	recipe, err := g.Compose(context.Background(), AIModels{ComposeMode: "lofi"}, &LyricsDraft{Lyrics: "l"})
	require.NoError(t, err)

	assert.Equal(t, "Lofi Chill", recipe.Title)
	assert.Equal(t, 70, recipe.Tempo)
	assert.Equal(t, "D minor", recipe.Key)
	assert.Equal(t, "Japanese female vocal, clear diction", recipe.VocalProfile)
	assert.Equal(t, []string{"synth"}, recipe.Instruments)
	require.Len(t, recipe.Sections, 1)
	assert.Equal(t, 0, recipe.Sections[0].StartSeconds)
	assert.Equal(t, 30, recipe.Sections[0].EndSeconds)
}

// TestComposeDefaultsComposeMode checks that when no mode is specified the prompt is built with
// the default mode. Passing an empty string as is would make a caller's prompt implementation
// that branches on the mode fail with no match.
func TestComposeDefaultsComposeMode(t *testing.T) {
	prompts := &stubTextPrompts{recipePrompt: "p"}
	g := newTextGenerator(textResponder(`{"title":"Song"}`), prompts)

	_, err := g.Compose(context.Background(), AIModels{}, &LyricsDraft{Lyrics: "l"})

	require.NoError(t, err)
	assert.Equal(t, defaultComposeMode, prompts.gotRecipeMode)
}

func TestComposeErrorBranches(t *testing.T) {
	tests := []struct {
		name        string
		prompts     *stubTextPrompts
		ai          *fakeGenerator
		wantErr     error
		wantAlsoErr error
		wantMessage string
	}{
		{
			name:        "prompt construction fails",
			prompts:     &stubTextPrompts{recipeErr: errors.New("prompt boom")},
			ai:          &fakeGenerator{},
			wantMessage: "failed to build prompt",
		},
		{
			name:        "AI returns an error",
			prompts:     &stubTextPrompts{recipePrompt: "p"},
			ai:          &fakeGenerator{err: errors.New("ai boom")},
			wantMessage: "compose generation failed",
		},
		{
			name:        "AI returns a nil response",
			prompts:     &stubTextPrompts{recipePrompt: "p"},
			ai:          &fakeGenerator{},
			wantErr:     ErrInvalidResponse,
			wantMessage: "compose response is nil",
		},
		{
			name:        "AI returns an empty string",
			prompts:     &stubTextPrompts{recipePrompt: "p"},
			ai:          textResponder(""),
			wantErr:     ErrInvalidResponse,
			wantAlsoErr: gemini.ErrEmptyResponse,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newTextGenerator(tt.ai, tt.prompts)

			_, err := g.Compose(context.Background(), AIModels{}, &LyricsDraft{Lyrics: "l"})

			require.Error(t, err)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			}
			if tt.wantAlsoErr != nil {
				assert.ErrorIs(t, err, tt.wantAlsoErr, "the gemini-side sentinel stays in the chain too")
			}
			if tt.wantMessage != "" {
				assert.Contains(t, err.Error(), tt.wantMessage)
			}
		})
	}
}

func TestComposeRejectsNilLyrics(t *testing.T) {
	g := newTextGenerator(&fakeGenerator{}, &stubTextPrompts{})

	_, err := g.Compose(context.Background(), AIModels{}, nil)

	assert.ErrorIs(t, err, ErrNilInput)
}

// TestGenerateJSONTruncatesRawOutputInError checks that raw output that could not be parsed is
// truncated in the error message. A recipe JSON runs to several KB, so embedding all of it
// bloats a single log line.
func TestGenerateJSONTruncatesRawOutputInError(t *testing.T) {
	long := "{" + strings.Repeat("x", 5000)
	g := newTextGenerator(textResponder(long), &stubTextPrompts{lyricsPrompt: "p"})

	_, err := g.GenerateLyrics(context.Background(), AIModels{}, &CollectedContent{Prompt: "x"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "…(truncated)")
	assert.Less(t, len(err.Error()), len(long), "the raw output is embedded in full")
}

// TestGenerateLyricsDeduplicatesConcurrentCalls checks that concurrent calls with the same content
// collapse into one, while each caller still receives an independent result.
//
// Returning the shared result as is would let one caller's modification be visible to the others.
func TestGenerateLyricsDeduplicatesConcurrentCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		ai := &fakeGenerator{
			block: release,
			resp:  &gemini.Response{Text: `{"title":"Song","theme":"Theme","hook":"h","lyrics":"Words","keywords":["one"]}`},
		}
		g := newTextGenerator(ai, &stubTextPrompts{lyricsPrompt: "lyrics prompt"})
		input := &CollectedContent{Prompt: "same input"}

		const callers = 5
		results := make([]*LyricsDraft, callers)
		errs := make([]error, callers)

		var wg sync.WaitGroup
		for i := range callers {
			wg.Go(func() {
				results[i], errs[i] = g.GenerateLyrics(context.Background(), AIModels{}, input)
			})
		}

		// Wait returns once every other goroutine in the bubble is blocked, so neither a wait time
		// for "they should have joined by now" nor polling is needed.
		synctest.Wait()
		close(release)
		wg.Wait()

		require.Equal(t, 1, ai.callCount(), "concurrent calls with the same content were not collapsed")
		for _, err := range errs {
			require.NoError(t, err)
		}

		// A copy is returned, not the shared result.
		require.NotSame(t, results[0], results[1])
		results[0].Keywords[0] = "changed"
		assert.Equal(t, "one", results[1].Keywords[0], "the internal slice is shared")
	})
}

// TestGenerateLyricsSeparatesDifferentSeeds checks that concurrent calls with different seeds
// do not share the result of one generation.
//
// The seed cannot be derived from the prompt yet changes the result, so if it were left out of
// the singleflight key, producing different results per seed would silently stop working.
func TestGenerateLyricsSeparatesDifferentSeeds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		ai := &fakeGenerator{block: release, resp: &gemini.Response{Text: validLyricsJSON}}
		g := newTextGenerator(ai, &stubTextPrompts{lyricsPrompt: "same prompt"})
		input := &CollectedContent{Prompt: "same input"}

		seedA, seedB := int64(1), int64(2)
		var wg sync.WaitGroup
		for _, seed := range []*int64{&seedA, &seedB} {
			wg.Go(func() {
				_, _ = g.GenerateLyrics(context.Background(), AIModels{Seed: seed}, input)
			})
		}

		// Wait until both are in flight. If they did not share a call, there are two calls.
		synctest.Wait()
		got := ai.callCount()

		close(release)
		wg.Wait()

		assert.Equal(t, 2, got, "calls with different seeds share one generation result")
	})
}
