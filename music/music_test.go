package music

import (
	"encoding/json"
	"testing"
)

// TestRecipeJSONWireFormat は、レシピ JSON のワイヤ形式が変わっていないことを
// 検証します。下流サービスはこの形式で GCS にレシピを永続化しているため、
// キー名の変化はデータ互換性の破壊になります。
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

	// 埋め込みの AIModels はトップレベルへ平坦化される（別オブジェクトにしない）。
	for _, key := range []string{
		"title", "theme", "mood", "tempo", "key", "vocal_profile",
		"instruments", "sections", "lyrics",
		"text_model", "audio_model", "seed", "lang",
	} {
		if _, ok := raw[key]; !ok {
			t.Errorf("キー %q がありません（ワイヤ形式が変わっています）: %s", key, data)
		}
	}
	if _, nested := raw["AIModels"]; nested {
		t.Errorf("AIModels が入れ子のオブジェクトになっています: %s", data)
	}

	section := raw["sections"].([]any)[0].(map[string]any)
	for _, key := range []string{"name", "duration_seconds", "start_seconds", "end_seconds", "prompt"} {
		if _, ok := section[key]; !ok {
			t.Errorf("セクションのキー %q がありません: %s", key, data)
		}
	}

	var back Recipe
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("往復の Unmarshal() error = %v", err)
	}
	if back.Title != r.Title || back.Seed == nil || *back.Seed != seed || len(back.Sections) != 1 {
		t.Errorf("往復で値が変わりました: %+v", back)
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
		t.Errorf("Clone() が浅いコピーになっています: %+v", cloned)
	}
}

// TestRecipeCloneKeepsSliceShape は、複製がスライスの nil と空を取り違えないことを
// 検証します。Instruments と Sections には omitempty が無いため、空スライスが nil へ
// 化けると保存済みレシピの JSON が [] から null に変わります。
func TestRecipeCloneKeepsSliceShape(t *testing.T) {
	filled := &Recipe{Instruments: []string{}, Sections: []Section{}}
	if got := filled.Clone(); got.Instruments == nil || got.Sections == nil {
		t.Errorf("空スライスが nil になりました: %+v", got)
	}

	var empty Recipe
	if got := empty.Clone(); got.Instruments != nil || got.Sections != nil {
		t.Errorf("nil スライスが空スライスになりました: %+v", got)
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
		t.Errorf("Clone() が浅いコピーになっています: %+v", cloned)
	}
}

// TestIsJapanese は、Lang 未指定を日本語扱いにすることを検証します。
// 既存のレシピは Lang を持たないため、未指定が英語扱いになると読み仮名変換が
// 黙って外れます。
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
