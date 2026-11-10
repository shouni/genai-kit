// Package music provides the data types that describe a song's structure (Recipe and related types).
//
// These types are shared as "the vocabulary of a song" not only by lyria's generation workflow
// but by many downstream services, such as video generation and history display. They are split
// out as a dependency-free leaf package so that the types can be referenced without importing
// the workflow itself (the lyria package, with its rate limiting and singleflight).
// For compatibility, the lyria package exposes aliases such as MusicRecipe = music.Recipe.
package music

import "slices"

// AIModels specifies the text and audio models used for song generation and the generation mode.
//
// It is embedded in Recipe, so its JSON tags use the same snake_case as Recipe's other
// fields. Without tags, the Go field names would be written to the recipe JSON as is.
type AIModels struct {
	TextModel   string `json:"text_model,omitempty"`
	AudioModel  string `json:"audio_model,omitempty"`
	LyricsMode  string `json:"lyrics_mode,omitempty"`
	ComposeMode string `json:"compose_mode,omitempty"`
	Seed        *int64 `json:"seed,omitempty"`
	// Lang is the language code of the lyrics and vocals ("ja" / "en"). Empty is treated as "ja".
	Lang string `json:"lang,omitempty"`
}

// LangJapanese and LangEnglish are the language codes that can be set on AIModels.Lang.
const (
	LangJapanese = "ja"
	LangEnglish  = "en"
)

// LyricsDraft is the structured lyrics output that serves as input to recipe generation.
type LyricsDraft struct {
	Title     string   `json:"title"`
	Theme     string   `json:"theme"`
	Hook      string   `json:"hook"`
	Lyrics    string   `json:"lyrics"`
	Keywords  []string `json:"keywords,omitempty"`
	Mood      string   `json:"mood,omitempty"`
	Narrative string   `json:"narrative,omitempty"`
}

// Clone copies the LyricsDraft so that the caller can modify it safely.
func (d *LyricsDraft) Clone() *LyricsDraft {
	if d == nil {
		return nil
	}

	dst := *d
	dst.Keywords = slices.Clone(d.Keywords)
	return &dst
}

// Recipe describes a song's structure and generation settings.
//
// The JSON tags are the wire format itself of the recipe JSON that downstream services store in GCS.
// Renaming a tag makes stored recipes unreadable.
//
// Only song vocabulary that applies to every service belongs here. Caller policy, such as the
// target model or how lyrics are written, belongs on the caller's own type that embeds Recipe. Once
// embedded, its fields sit at the same level in JSON, so the recipe JSON stays a single object.
type Recipe struct {
	Title        string       `json:"title"`
	Theme        string       `json:"theme"`
	Mood         string       `json:"mood"`
	Tempo        int          `json:"tempo"`
	Key          string       `json:"key,omitempty"`
	VocalProfile string       `json:"vocal_profile,omitempty"`
	Instruments  []string     `json:"instruments"`
	Sections     []Section    `json:"sections"`
	Lyrics       *LyricsDraft `json:"lyrics,omitempty"`
	AIModels
}

// IsJapanese reports whether this recipe is a Japanese song. An unspecified Lang is treated as Japanese.
func (r *Recipe) IsJapanese() bool {
	return r.Lang == "" || r.Lang == LangJapanese
}

// Clone copies the Recipe along with its internal slices and pointers.
//
// Slices are copied with slices.Clone. nil stays nil and empty stays empty, so the JSON wire
// format (null versus [] for instruments) does not change through a copy.
func (r *Recipe) Clone() *Recipe {
	if r == nil {
		return nil
	}

	dst := *r
	dst.Instruments = slices.Clone(r.Instruments)
	dst.Sections = slices.Clone(r.Sections)
	dst.Lyrics = r.Lyrics.Clone()
	if r.Seed != nil {
		v := *r.Seed
		dst.Seed = &v
	}
	return &dst
}

// Section is one section of a song (Verse, Chorus, etc.).
type Section struct {
	Name         string `json:"name"`
	Duration     int    `json:"duration_seconds"`
	StartSeconds int    `json:"start_seconds"`
	EndSeconds   int    `json:"end_seconds"`
	Prompt       string `json:"prompt"`
}
