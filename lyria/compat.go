package lyria

import (
	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/music"
)

// The data types for a song's structure have been split out into the music package.
//
// This lets downstream code that only wants the types (video generation, history display, etc.)
// avoid importing the workflow itself (this package, with its rate limiting and singleflight).
// The aliases here are a compatibility layer so that existing spellings such as lyria.MusicRecipe
// keep working; whichever name is used, it is the same type.
// The types themselves are documented in music. Copying that here would inevitably drift.
type (
	// AIModels is an alias of music.AIModels.
	AIModels = music.AIModels
	// LyricsDraft is an alias of music.LyricsDraft.
	LyricsDraft = music.LyricsDraft
	// MusicRecipe is an alias of music.Recipe.
	MusicRecipe = music.Recipe
	// MusicSection is an alias of music.Section.
	MusicSection = music.Section
)

// LangJapanese and LangEnglish are the language codes that can be set on MusicRecipe.Lang.
const (
	LangJapanese = music.LangJapanese
	LangEnglish  = music.LangEnglish
)

// ImagePayload is optional multimodal image input attached to audio generation.
//
// It is an alias of gemini.Attachment. Defining the same concept, "a MIME type and bytes", in two
// places would force callers to copy the same content from one to the other. Because it is an alias,
// existing code such as lyria.ImagePayload{Data: ..., MIMEType: ...} can be written as is.
type ImagePayload = gemini.Attachment
