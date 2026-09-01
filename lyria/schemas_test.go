package lyria

import (
	"testing"

	"github.com/shouni/genai-kit/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLyricsDraftSchemaRequiresBody は、歌詞本文を含む必須項目がスキーマに
// 定義されていることを検証します。required から漏れるとモデルが省略でき、
// 生成後の空チェック（ErrEmptyLyrics）でしか気付けません。
func TestLyricsDraftSchemaRequiresBody(t *testing.T) {
	schema := lyricsDraftSchema()

	assert.Equal(t, gemini.TypeObject, schema.Type)
	for _, field := range []string{"title", "theme", "hook", "lyrics"} {
		assert.Contains(t, schema.Required, field)
		_, defined := schema.Properties[field]
		assert.True(t, defined, "必須項目 %q が properties にありません", field)
	}

	// 任意項目も型は定義しておく（未定義だとモデルが自由な形で返す）。
	for _, field := range []string{"keywords", "mood", "narrative"} {
		_, defined := schema.Properties[field]
		assert.True(t, defined, "任意項目 %q が properties にありません", field)
	}
}

// TestMusicRecipeSchemaRequiresSectionTimeline は、sections の timeline フィールドが
// 構造化出力スキーマで必須になっていることを保証します。
// required から漏れるとモデルが start/end_seconds を省略でき、生成レシピの再利用時に
// timeline 検証で弾かれる不整合が起きます。
func TestMusicRecipeSchemaRequiresSectionTimeline(t *testing.T) {
	schema := musicRecipeSchema()

	sections, ok := schema.Properties["sections"]
	require.True(t, ok, "sections プロパティがありません")
	require.NotNil(t, sections.Items)

	for _, field := range []string{"name", "duration_seconds", "start_seconds", "end_seconds", "prompt"} {
		assert.Contains(t, sections.Items.Required, field)
		_, defined := sections.Items.Properties[field]
		assert.True(t, defined, "必須項目 %q が properties にありません", field)
	}
}

// TestMusicRecipeSchemaOmitsGeneratedFields は、歌詞と AIModels をスキーマから
// 意図的に外していることを検証します。
//
// これらはモデルに生成させず、生成後にコードが付けます。スキーマに含めると
// モデルがモデル名やシードを創作し、レシピの再現性の記録が壊れます。
func TestMusicRecipeSchemaOmitsGeneratedFields(t *testing.T) {
	schema := musicRecipeSchema()

	for _, field := range []string{"lyrics", "text_model", "audio_model", "seed", "lang", "lyrics_mode", "compose_mode"} {
		_, defined := schema.Properties[field]
		assert.False(t, defined, "%q はモデルに生成させない項目です", field)
	}
}
