package lyria

import (
	"testing"

	"github.com/shouni/genai-kit/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLyricsDraftSchemaRequiresBody checks that the required fields, including the lyrics body,
// are defined in the schema. If one drops out of required, the model can omit it, and it is
// only noticed by the post-generation empty check (ErrEmptyLyrics).
func TestLyricsDraftSchemaRequiresBody(t *testing.T) {
	schema := lyricsDraftSchema()

	assert.Equal(t, gemini.TypeObject, schema.Type)
	for _, field := range []string{"title", "theme", "hook", "lyrics"} {
		assert.Contains(t, schema.Required, field)
		_, defined := schema.Properties[field]
		assert.True(t, defined, "required field %q is missing from properties", field)
	}

	// Optional fields get a type too (if undefined, the model returns them in any shape).
	for _, field := range []string{"keywords", "mood", "narrative"} {
		_, defined := schema.Properties[field]
		assert.True(t, defined, "optional field %q is missing from properties", field)
	}
}

// TestMusicRecipeSchemaRequiresSectionTimeline ensures that the sections' timeline fields
// are required in the structured-output schema.
// If they drop out of required, the model can omit start/end_seconds, and reusing the
// generated recipe fails the timeline validation.
func TestMusicRecipeSchemaRequiresSectionTimeline(t *testing.T) {
	schema := musicRecipeSchema()

	sections, ok := schema.Properties["sections"]
	require.True(t, ok, "sections property is missing")
	require.NotNil(t, sections.Items)

	for _, field := range []string{"name", "duration_seconds", "start_seconds", "end_seconds", "prompt"} {
		assert.Contains(t, sections.Items.Required, field)
		_, defined := sections.Items.Properties[field]
		assert.True(t, defined, "required field %q is missing from properties", field)
	}
}

// TestMusicRecipeSchemaOmitsGeneratedFields checks that lyrics and AIModels are deliberately
// left out of the schema.
//
// The model does not generate these; code adds them after generation. Including them in the
// schema would let the model invent model names and seeds, breaking the recipe's reproducibility record.
func TestMusicRecipeSchemaOmitsGeneratedFields(t *testing.T) {
	schema := musicRecipeSchema()

	for _, field := range []string{"lyrics", "text_model", "audio_model", "seed", "lang", "lyrics_mode", "compose_mode"} {
		_, defined := schema.Properties[field]
		assert.False(t, defined, "%q is a field the model must not generate", field)
	}
}
