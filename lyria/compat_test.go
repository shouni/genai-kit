package lyria

import (
	"testing"

	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/music"
	"github.com/stretchr/testify/assert"
)

// The compatibility layer's aliases are "the same type", not distinct types. That this
// assignment compiles is itself the check; it breaks the moment either side becomes its own type.
// As a distinct type, downstream code could not pass a music value without converting it.
var (
	_ MusicRecipe  = music.Recipe{}
	_ LyricsDraft  = music.LyricsDraft{}
	_ AIModels     = music.AIModels{}
	_ MusicSection = music.Section{}
	_ ImagePayload = gemini.Attachment{}
)

func TestCompatAliasesShareValues(t *testing.T) {
	// A music / gemini value can be passed as is to a slot declared with the alias.
	// With a distinct type a conversion would be needed, and this would not compile.
	title := func(r *MusicRecipe) string { return r.Title }
	assert.Equal(t, "Song", title(&music.Recipe{Title: "Song"}))

	mimeType := func(p ImagePayload) string { return p.MIMEType }
	assert.Equal(t, "image/png", mimeType(gemini.Attachment{MIMEType: "image/png", Data: []byte("x")}))

	// The language-code constants must have the same values as on the music side.
	assert.Equal(t, music.LangJapanese, LangJapanese)
	assert.Equal(t, music.LangEnglish, LangEnglish)
}
