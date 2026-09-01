package lyria

import (
	"testing"

	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/music"
	"github.com/stretchr/testify/assert"
)

// 互換層の別名は「同じ型」であって別型ではありません。この代入がコンパイルできること
// 自体が検証で、どちらかを独立した型に変えた時点でここが壊れます。
// 別型になると、下流は music 側の値を詰め替えないと渡せなくなります。
var (
	_ MusicRecipe  = music.Recipe{}
	_ LyricsDraft  = music.LyricsDraft{}
	_ AIModels     = music.AIModels{}
	_ MusicSection = music.Section{}
	_ ImagePayload = gemini.Attachment{}
)

func TestCompatAliasesShareValues(t *testing.T) {
	// 別名で宣言した口へ、music / gemini 側の値をそのまま渡せる。
	// 別型なら詰め替えが必要になり、ここがコンパイルできなくなる。
	title := func(r *MusicRecipe) string { return r.Title }
	assert.Equal(t, "Song", title(&music.Recipe{Title: "Song"}))

	mimeType := func(p ImagePayload) string { return p.MIMEType }
	assert.Equal(t, "image/png", mimeType(gemini.Attachment{MIMEType: "image/png", Data: []byte("x")}))

	// 言語コードの定数も music 側と同じ値であること。
	assert.Equal(t, music.LangJapanese, LangJapanese)
	assert.Equal(t, music.LangEnglish, LangEnglish)
}
