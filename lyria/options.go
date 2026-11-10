package lyria

import (
	"time"

	"github.com/shouni/genai-kit/gemini"
)

type options struct {
	geminiModel      string
	lyriaModel       string
	rateInterval     time.Duration
	textRateInterval time.Duration
	execTimeout      time.Duration
	audioGenerator   gemini.Generator
}

// Option is a construction-time setting for Workflow.
type Option func(*options)

// WithGeminiModel specifies the model used for lyrics generation and recipe generation.
func WithGeminiModel(value string) Option {
	return func(opts *options) {
		opts.geminiModel = value
	}
}

// WithLyriaModel specifies the model used for audio generation.
func WithLyriaModel(value string) Option {
	return func(opts *options) {
		opts.lyriaModel = value
	}
}

// WithAudioGenerator replaces the generation client used only for audio generation (Lyria).
// If unspecified, the aiClient passed to New is shared with lyrics and composition.
//
// generateContent cannot choose Lyria's output format, so this is the hook for diverting audio
// alone onto the Interactions path. If you want WAV, pass a generator made with
// gemini.Client.Interactions:
//
//	lyria.WithAudioGenerator(client.Interactions(gemini.InteractionsConfig{AudioMIMEType: "audio/wav"}))
//
// The replacement point is placed here (gemini.Generator) so that the Workflow, Track, call
// guard and prompt construction are all reused as is, and reverting is just a matter of
// removing this option.
func WithAudioGenerator(g gemini.Generator) Option {
	return func(opts *options) {
		opts.audioGenerator = g
	}
}

// WithRateInterval specifies the firing interval for audio generation. If unset, there is no limit.
func WithRateInterval(value time.Duration) Option {
	return func(opts *options) {
		opts.rateInterval = value
	}
}

// WithTextRateInterval specifies the firing interval for lyrics and recipe generation (text).
// If unset, there is no limit. It is kept separate from audio because it is a different quota of a different model.
func WithTextRateInterval(value time.Duration) Option {
	return func(opts *options) {
		opts.textRateInterval = value
	}
}

// WithExecTimeout is the time limit for one generation call shared through singleflight.
// A shared execution is detached from the caller's context, so this is the only way to cut it
// off. If unset, callguard.DefaultExecTimeout applies.
//
// Waiting for the firing interval is not counted in this time (see callguard.Do).
func WithExecTimeout(value time.Duration) Option {
	return func(opts *options) {
		opts.execTimeout = value
	}
}

func applyOptions(overrides ...Option) options {
	var opts options
	for _, override := range overrides {
		if override != nil {
			override(&opts)
		}
	}
	return opts
}

// jsonOptions returns options optimised for generating structured JSON data with Gemini.
// Specifying schema enables structured output (constrained decoding), constraining the output at the grammar level.
func jsonOptions(seed *int64, schema *gemini.Schema) gemini.GenerateOptions {
	opts := baseOptions(seed, "application/json")
	opts.ResponseSchema = schema
	return opts
}

// audioOptions returns options optimised for audio generation with Lyria.
// Lyria models return audio without a response MIME type, so none is specified.
func audioOptions(seed *int64) gemini.GenerateOptions {
	return baseOptions(seed, "")
}

// baseOptions builds base options with the package-wide safety settings and seed applied.
// NOTE: to favour reproducible generation, the block threshold of every supported category is set to BlockNone.
// Controlling input and output is assumed to be done by the caller or a later stage.
func baseOptions(seed *int64, mimeType string) gemini.GenerateOptions {
	opts := gemini.GenerateOptions{
		SafetySettings: gemini.NewSafetySettings(gemini.SafetyBlockNone),
	}
	if seed != nil {
		opts.Seed = seed
	}
	if mimeType != "" {
		opts.ResponseMIMEType = mimeType
	}
	return opts
}
