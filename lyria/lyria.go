// Package lyria provides a music generation workflow that brings together lyrics
// generation, song design and audio generation with Lyria.
//
// The three stages (GenerateLyrics / Compose / GenerateAudio) are exposed as separate methods,
// and there is deliberately no one-shot entry point. The quality gates placed between stages
// differ per product, so bundling them would only make callers take them apart again.
//
// The song types themselves are in the music package (MusicRecipe and the like are aliases of them).
package lyria

import (
	"context"
	"fmt"

	"github.com/shouni/genai-kit/callguard"
	"github.com/shouni/genai-kit/gemini"
)

// These assertions guarantee at compile time that Workflow satisfies the package's public interfaces.
// Without them, a drifting method signature would go unnoticed until a downstream
// caller is built.
var (
	_ LyricsGenerator = (*Workflow)(nil)
	_ Composer        = (*Workflow)(nil)
	_ AudioGenerator  = (*Workflow)(nil)
)

// Workflow is a facade that brings together lyrics generation, composition and audio generation.
type Workflow struct {
	lyrics   LyricsGenerator
	composer Composer
	audio    AudioGenerator
}

// New creates a Workflow, injecting the AI client and the implementation of the lyrics and composition prompts.
// The prompt for audio generation is assembled by the caller and passed to GenerateAudio (AudioRequest).
//
// Always specify the model names with WithGeminiModel / WithLyriaModel.
// There are no defaults because the choice of model directly determines quality and cost.
func New(aiClient gemini.Generator, textPrompts TextPromptBuilder, overrides ...Option) (*Workflow, error) {
	opts := applyOptions(overrides...)
	if aiClient == nil {
		return nil, fmt.Errorf("%w: aiClient is required", ErrWorkflowConfig)
	}
	if textPrompts == nil {
		return nil, fmt.Errorf("%w: textPrompts is required", ErrWorkflowConfig)
	}
	if opts.geminiModel == "" {
		return nil, fmt.Errorf("%w: GeminiModel is required but not set", ErrWorkflowConfig)
	}
	if opts.lyriaModel == "" {
		return nil, fmt.Errorf("%w: LyriaModel is required but not set", ErrWorkflowConfig)
	}

	// The firing interval is kept separately for text (Gemini) and audio (Lyria). They are
	// different quotas of different models, so there is no reason to throttle one when the other is busy.
	textGuard := callguard.New(
		callguard.WithRateInterval(opts.textRateInterval),
		callguard.WithExecTimeout(opts.execTimeout),
	)
	audioGuard := callguard.New(
		callguard.WithRateInterval(opts.rateInterval),
		callguard.WithExecTimeout(opts.execTimeout),
	)

	textGen := &textGenerator{
		aiClient:     aiClient,
		prompts:      textPrompts,
		defaultModel: opts.geminiModel,
		guard:        textGuard,
	}

	// Only audio can be diverted to a different client (see WithAudioGenerator).
	audioClient := opts.audioGenerator
	if audioClient == nil {
		audioClient = aiClient
	}

	return &Workflow{
		lyrics:   textGen,
		composer: textGen,
		audio: &audioGenerator{
			aiClient:          audioClient,
			guard:             audioGuard,
			defaultLyriaModel: opts.lyriaModel,
		},
	}, nil
}

// GenerateLyrics generates a lyrics draft from collected content.
func (w *Workflow) GenerateLyrics(ctx context.Context, ai AIModels, input *CollectedContent) (*LyricsDraft, error) {
	return w.lyrics.GenerateLyrics(ctx, ai, input)
}

// Compose generates a song recipe from a lyrics draft.
func (w *Workflow) Compose(ctx context.Context, ai AIModels, lyrics *LyricsDraft) (*MusicRecipe, error) {
	return w.composer.Compose(ctx, ai, lyrics)
}

// GenerateAudio generates the audio for the whole song from an assembled prompt.
func (w *Workflow) GenerateAudio(ctx context.Context, req AudioRequest) (*Track, error) {
	return w.audio.GenerateAudio(ctx, req)
}
