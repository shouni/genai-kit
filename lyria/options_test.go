package lyria

import (
	"testing"
	"time"

	"github.com/shouni/genai-kit/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyOptions(t *testing.T) {
	t.Parallel()

	t.Run("applies the specified values", func(t *testing.T) {
		audio := &fakeGenerator{}

		got := applyOptions(
			WithGeminiModel("gemini-flash"),
			WithLyriaModel("lyria-3"),
			WithRateInterval(250*time.Millisecond),
			WithTextRateInterval(100*time.Millisecond),
			WithExecTimeout(90*time.Second),
			WithAudioGenerator(audio),
		)

		assert.Same(t, audio, got.audioGenerator)
		assert.Equal(t, "gemini-flash", got.geminiModel)
		assert.Equal(t, "lyria-3", got.lyriaModel)
		assert.Equal(t, 250*time.Millisecond, got.rateInterval)
		assert.Equal(t, 100*time.Millisecond, got.textRateInterval)
		assert.Equal(t, 90*time.Second, got.execTimeout)
	})

	// An execTimeout of 0 means "unset" and falls through to callguard's default.
	// Filling in a default here would mean managing it in two places alongside callguard's.
	t.Run("leaves unspecified values at zero", func(t *testing.T) {
		got := applyOptions()

		assert.Zero(t, got.execTimeout)
		assert.Zero(t, got.rateInterval)
		assert.Zero(t, got.textRateInterval)
		assert.Nil(t, got.audioGenerator)
	})

	t.Run("skips a nil Option", func(t *testing.T) {
		got := applyOptions(nil, WithGeminiModel("g"), nil)

		assert.Equal(t, "g", got.geminiModel)
	})
}

// TestJSONOptions checks that the structured-output settings are all present.
// If the schema is dropped, the model output is no longer constrained at the grammar level.
func TestJSONOptions(t *testing.T) {
	t.Parallel()

	schema := lyricsDraftSchema()
	seed := int64(42)

	got := jsonOptions(&seed, schema)

	assert.Equal(t, "application/json", got.ResponseMIMEType)
	assert.Same(t, schema, got.ResponseSchema)
	if assert.NotNil(t, got.Seed) {
		assert.Equal(t, seed, *got.Seed)
	}
}

// TestAudioOptions checks the options for Lyria.
// Lyria returns audio without a response MIME type, so none is specified.
func TestAudioOptions(t *testing.T) {
	t.Parallel()

	t.Run("does not pass seed when it is nil", func(t *testing.T) {
		got := audioOptions(nil)

		assert.Nil(t, got.Seed)
		assert.Empty(t, got.ResponseMIMEType)
	})

	t.Run("keeps seed", func(t *testing.T) {
		seed := int64(42)

		got := audioOptions(&seed)

		require.NotNil(t, got.Seed)
		assert.Equal(t, seed, *got.Seed)
		assert.Empty(t, got.ResponseMIMEType)
	})
}

// TestBaseOptionsUsesBlockNone checks that the shared safety settings are uniformly
// BLOCK_NONE. The choice favours reproducible generation, on the premise that the caller
// or a later stage takes care of controlling input and output.
func TestBaseOptionsUsesBlockNone(t *testing.T) {
	t.Parallel()

	got := baseOptions(nil, "")

	require.NotEmpty(t, got.SafetySettings)
	for _, setting := range got.SafetySettings {
		assert.Equal(t, gemini.SafetyBlockNone, setting.Threshold)
	}
}
