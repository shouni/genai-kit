package lyria

import "github.com/shouni/genai-kit/gemini"

// lyricsDraftSchema は LyricsDraft の構造化出力スキーマです。
// ResponseMIMEType "application/json" と併用することで、モデル出力が
// このスキーマに文法レベルで制約されます。
func lyricsDraftSchema() *gemini.Schema {
	return &gemini.Schema{
		Type: gemini.TypeObject,
		Properties: map[string]*gemini.Schema{
			"title":     {Type: gemini.TypeString},
			"theme":     {Type: gemini.TypeString},
			"hook":      {Type: gemini.TypeString},
			"lyrics":    {Type: gemini.TypeString},
			"keywords":  {Type: gemini.TypeArray, Items: &gemini.Schema{Type: gemini.TypeString}},
			"mood":      {Type: gemini.TypeString},
			"narrative": {Type: gemini.TypeString},
		},
		Required: []string{"title", "theme", "hook", "lyrics"},
	}
}

// musicRecipeSchema は MusicRecipe の構造化出力スキーマです。
// Lyrics と AIModels はモデルに生成させず、呼び出し側のコードが付与するため
// 意図的にスキーマへ含めていません。
func musicRecipeSchema() *gemini.Schema {
	return &gemini.Schema{
		Type: gemini.TypeObject,
		Properties: map[string]*gemini.Schema{
			"title":         {Type: gemini.TypeString},
			"theme":         {Type: gemini.TypeString},
			"mood":          {Type: gemini.TypeString},
			"tempo":         {Type: gemini.TypeInteger},
			"key":           {Type: gemini.TypeString},
			"vocal_profile": {Type: gemini.TypeString},
			"instruments":   {Type: gemini.TypeArray, Items: &gemini.Schema{Type: gemini.TypeString}},
			"sections": {
				Type: gemini.TypeArray,
				Items: &gemini.Schema{
					Type: gemini.TypeObject,
					Properties: map[string]*gemini.Schema{
						"name":             {Type: gemini.TypeString},
						"duration_seconds": {Type: gemini.TypeInteger},
						"start_seconds":    {Type: gemini.TypeInteger},
						"end_seconds":      {Type: gemini.TypeInteger},
						"prompt":           {Type: gemini.TypeString},
					},
					Required: []string{"name", "duration_seconds", "start_seconds", "end_seconds", "prompt"},
				},
			},
		},
		Required: []string{"title", "theme", "mood", "tempo", "instruments", "sections"},
	}
}
