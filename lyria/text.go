package lyria

import (
	"context"
	"fmt"
	"strings"

	"github.com/shouni/genai-kit/callguard"
	"github.com/shouni/genai-kit/gemini"
)

// LyricsGenerator is the role responsible for generating lyrics.
type LyricsGenerator interface {
	GenerateLyrics(ctx context.Context, ai AIModels, input *CollectedContent) (*LyricsDraft, error)
}

// Composer is the role responsible for designing the song (building the recipe).
type Composer interface {
	Compose(ctx context.Context, ai AIModels, lyrics *LyricsDraft) (*MusicRecipe, error)
}

// TextPromptBuilder is the interface that builds prompts for lyrics and recipe generation.
//
// The methods return prompt strings, not lyrics or recipes. A name starting with Generate
// would be confused with LyricsGenerator.GenerateLyrics (the side that generates lyrics), so
// the names say with a noun "which prompt it is". In this package, a Generator is the side
// that calls the AI, and a Builder is an assembler that does not.
type TextPromptBuilder interface {
	LyricsPrompt(mode string, input string) (string, error)
	RecipePrompt(mode string, lyrics *LyricsDraft) (string, error)
}

// CollectedContent is the text and images that serve as input to lyrics and song generation.
type CollectedContent struct {
	Prompt string
	Images []ImagePayload
}

const defaultComposeMode = "default"

// textGenerator handles both lyrics generation and song recipe generation with Gemini.
type textGenerator struct {
	aiClient     gemini.Generator
	prompts      TextPromptBuilder
	defaultModel string
	// A nil guard means "no limits, default time limit".
	guard *callguard.Guard
	group callguard.Group
}

// resolveModel returns the per-call model if one is specified, otherwise the default model.
func (g *textGenerator) resolveModel(override string) string {
	if override != "" {
		return override
	}
	return g.defaultModel
}

// generateJSON runs the "singleflight → Gemini call → JSON decode" flow shared by lyrics and
// recipe generation. kind identifies the call in error messages and the singleflight key.
// The return value is shared through singleflight, so callers must copy it before returning it.
//
// Since Go 1.27 methods can have type parameters, so this can be written as a method rather
// than a function taking the receiver as its second argument.
func (g *textGenerator) generateJSON[T any](ctx context.Context, kind, model, prompt string, seed *int64, schema *gemini.Schema) (*T, error) {
	// seed changes the result, so it must always be part of the key. If it were left out, concurrent
	// calls with the same prompt but different seeds would share the result of a single generation.
	key := callguard.Key(kind, model, prompt, callguard.SeedKey(seed))
	return callguard.Do(ctx, &g.group, g.guard, key, func(execCtx context.Context) (*T, error) {
		resp, err := g.aiClient.Generate(execCtx, model, prompt, nil, jsonOptions(seed, schema))
		if err != nil {
			return nil, fmt.Errorf("%s generation failed (model: %s): %w", kind, model, err)
		}
		if resp == nil {
			return nil, fmt.Errorf("%w: %s response is nil", ErrInvalidResponse, kind)
		}

		// The empty check, repair, decode and excerpt belong to gemini.DecodeJSON. Here we only
		// add this package's sentinel (ErrInvalidResponse) to the chain.
		out, err := gemini.DecodeJSON[T](resp.Text)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidResponse, kind, err)
		}
		return &out, nil
	})
}

// GenerateLyrics generates a lyrics draft from collected content.
func (g *textGenerator) GenerateLyrics(ctx context.Context, ai AIModels, input *CollectedContent) (*LyricsDraft, error) {
	if input == nil {
		return nil, fmt.Errorf("%w: collected content", ErrNilInput)
	}

	promptText, err := g.prompts.LyricsPrompt(ai.LyricsMode, input.Prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to build lyrics prompt: %w", err)
	}

	lyrics, err := g.generateJSON[LyricsDraft](ctx, "lyrics", g.resolveModel(ai.TextModel), promptText, ai.Seed, lyricsDraftSchema())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(lyrics.Lyrics) == "" {
		return nil, ErrEmptyLyrics
	}

	return lyrics.Clone(), nil
}

// Compose generates a song recipe from a lyrics draft.
func (g *textGenerator) Compose(ctx context.Context, ai AIModels, lyrics *LyricsDraft) (*MusicRecipe, error) {
	if lyrics == nil {
		return nil, fmt.Errorf("%w: lyrics draft", ErrNilInput)
	}

	targetMode := ai.ComposeMode
	if targetMode == "" {
		targetMode = defaultComposeMode
	}

	promptText, err := g.prompts.RecipePrompt(targetMode, lyrics)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt (mode: %s): %w", targetMode, err)
	}

	shared, err := g.generateJSON[MusicRecipe](ctx, "compose", g.resolveModel(ai.TextModel), promptText, ai.Seed, musicRecipeSchema())
	if err != nil {
		return nil, err
	}

	// Caller-specific information is added after copying the shared result.
	recipe := shared.Clone()
	recipe.Lyrics = lyrics.Clone()
	recipe.AIModels = ai
	if ai.Seed != nil {
		seed := *ai.Seed
		recipe.Seed = &seed
	}
	return recipe, nil
}
