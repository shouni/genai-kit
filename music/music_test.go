package music

import (
	"encoding/json"
	"testing"
)

// TestRecipeJSONWireFormat checks that the recipe JSON wire format has not changed.
// Downstream services persist recipes to GCS in this format, so a change in key names
// breaks data compatibility.
func TestRecipeJSONWireFormat(t *testing.T) {
	seed := int64(42)
	r := Recipe{
		Title:        "Song",
		Theme:        "theme",
		Mood:         "mood",
		Tempo:        120,
		Key:          "A minor",
		VocalProfile: "clear",
		Instruments:  []string{"synth"},
		Sections: []Section{
			{Name: "Verse", Duration: 30, StartSeconds: 0, EndSeconds: 30, Prompt: "pulse"},
		},
		Lyrics: &LyricsDraft{Title: "Song", Theme: "theme", Hook: "hook", Lyrics: "words"},
		AIModels: AIModels{
			TextModel:  "gemini-test",
			AudioModel: "lyria-test",
			Seed:       &seed,
			Lang:       LangJapanese,
		},
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	// The embedded AIModels is flattened to the top level (not a separate object).
	for _, key := range []string{
		"title", "theme", "mood", "tempo", "key", "vocal_profile",
		"instruments", "sections", "lyrics",
		"text_model", "audio_model", "seed", "lang",
	} {
		if _, ok := raw[key]; !ok {
			t.Errorf("key %q is missing (the wire format has changed): %s", key, data)
		}
	}
	if _, nested := raw["AIModels"]; nested {
		t.Errorf("AIModels has become a nested object: %s", data)
	}

	section := raw["sections"].([]any)[0].(map[string]any)
	for _, key := range []string{"name", "duration_seconds", "start_seconds", "end_seconds", "prompt"} {
		if _, ok := section[key]; !ok {
			t.Errorf("section key %q is missing: %s", key, data)
		}
	}

	var back Recipe
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("round-trip Unmarshal() error = %v", err)
	}
	if back.Title != r.Title || back.Seed == nil || *back.Seed != seed || len(back.Sections) != 1 {
		t.Errorf("the value changed in the round trip: %+v", back)
	}
}

func TestRecipeCloneIsDeep(t *testing.T) {
	seed := int64(7)
	src := &Recipe{
		Instruments: []string{"synth"},
		Sections:    []Section{{Name: "Verse"}},
		Lyrics:      &LyricsDraft{Keywords: []string{"one"}},
		AIModels:    AIModels{Seed: &seed},
	}

	cloned := src.Clone()
	src.Instruments[0] = "guitar"
	src.Sections[0].Name = "Chorus"
	src.Lyrics.Keywords[0] = "changed"
	*src.Seed = 99

	if cloned.Instruments[0] != "synth" || cloned.Sections[0].Name != "Verse" ||
		cloned.Lyrics.Keywords[0] != "one" || *cloned.Seed != 7 {
		t.Errorf("Clone() made a shallow copy: %+v", cloned)
	}
}

// TestRecipeCloneKeepsSliceShape checks that a copy does not confuse nil and empty slices.
// Instruments and Sections have no omitempty, so if an empty slice turned into nil, a stored
// recipe's JSON would change from [] to null.
func TestRecipeCloneKeepsSliceShape(t *testing.T) {
	filled := &Recipe{Instruments: []string{}, Sections: []Section{}}
	if got := filled.Clone(); got.Instruments == nil || got.Sections == nil {
		t.Errorf("an empty slice became nil: %+v", got)
	}

	var empty Recipe
	if got := empty.Clone(); got.Instruments != nil || got.Sections != nil {
		t.Errorf("a nil slice became an empty slice: %+v", got)
	}
}

func TestCloneHandlesNilReceiver(t *testing.T) {
	var recipe *Recipe
	if got := recipe.Clone(); got != nil {
		t.Errorf("Recipe.Clone() = %+v, want nil", got)
	}

	var draft *LyricsDraft
	if got := draft.Clone(); got != nil {
		t.Errorf("LyricsDraft.Clone() = %+v, want nil", got)
	}
}

func TestLyricsDraftCloneIsDeep(t *testing.T) {
	src := &LyricsDraft{Title: "Song", Keywords: []string{"one", "two"}}

	cloned := src.Clone()
	src.Keywords[0] = "changed"

	if cloned.Keywords[0] != "one" {
		t.Errorf("Clone() made a shallow copy: %+v", cloned)
	}
}

// TestIsJapanese checks that an unspecified Lang is treated as Japanese.
// Existing recipes have no Lang, so if unspecified were treated as English, the reading
// conversion would silently stop applying.
func TestIsJapanese(t *testing.T) {
	tests := []struct {
		lang string
		want bool
	}{
		{"", true},
		{LangJapanese, true},
		{LangEnglish, false},
	}

	for _, tt := range tests {
		r := &Recipe{AIModels: AIModels{Lang: tt.lang}}
		if got := r.IsJapanese(); got != tt.want {
			t.Errorf("IsJapanese(lang=%q) = %v, want %v", tt.lang, got, tt.want)
		}
	}
}
