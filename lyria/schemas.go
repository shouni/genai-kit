package lyria

import "github.com/shouni/genai-kit/gemini"

// lyricsDraftSchema is the structured-output schema for LyricsDraft.
// Used together with ResponseMIMEType "application/json", it constrains the model
// output to this schema at the grammar level.
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

// musicRecipeSchema is the structured-output schema for MusicRecipe.
// Lyrics and AIModels are deliberately left out of the schema: the model does not
// generate them, the caller's code fills them in.
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
