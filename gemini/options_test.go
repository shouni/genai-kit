package gemini

import (
	"testing"

	"google.golang.org/genai"
)

func TestGenerateOptionsHasImageConfig(t *testing.T) {
	tests := []struct {
		name string
		opts *GenerateOptions
		want bool
	}{
		{"nil レシーバ", nil, false},
		{"設定なし", &GenerateOptions{}, false},
		{"AspectRatio あり", &GenerateOptions{AspectRatio: "16:9"}, true},
		{"ImageSize あり", &GenerateOptions{ImageSize: "1K"}, true},
		{"PersonGeneration あり", &GenerateOptions{PersonGeneration: PersonGenerationAllowAll}, true},
		{"画像以外のみ", &GenerateOptions{SystemPrompt: "test"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.opts.HasImageConfig(); got != tt.want {
				t.Errorf("HasImageConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestNewSafetySettings は、標準の 4 カテゴリすべてに同じ閾値が載ることを検証します。
// 1 つでも漏れるとそのカテゴリだけ API 既定の閾値で動き、意図した緩和／厳格化が
// 部分的にしか効きません。
func TestNewSafetySettings(t *testing.T) {
	got := NewSafetySettings(SafetyBlockNone)

	want := []genai.HarmCategory{
		genai.HarmCategoryHarassment,
		genai.HarmCategoryHateSpeech,
		genai.HarmCategorySexuallyExplicit,
		genai.HarmCategoryDangerousContent,
	}
	if len(got) != len(want) {
		t.Fatalf("settings = %d 件, want %d 件", len(got), len(want))
	}
	for i, category := range want {
		if got[i].Category != category {
			t.Errorf("settings[%d].Category = %v, want %v", i, got[i].Category, category)
		}
		if got[i].Threshold != SafetyBlockNone {
			t.Errorf("settings[%d].Threshold = %v, want %v", i, got[i].Threshold, SafetyBlockNone)
		}
	}
}

// TestSchemaAliasReachesSDKUnchanged は、このパッケージの別名で書いたスキーマが
// そのまま SDK へ渡ることを検証します。
//
// 別名を用意したのは、構造化出力のためだけに下流が genai を import するのを
// 止めるためです。それが成り立つのは「同じ型」である場合だけなので、
// この比較がコンパイルできること自体が検証になっています。
func TestSchemaAliasReachesSDKUnchanged(t *testing.T) {
	schema := &Schema{
		Type: TypeObject,
		Properties: map[string]*Schema{
			"title":    {Type: TypeString},
			"keywords": {Type: TypeArray, Items: &Schema{Type: TypeString}},
		},
		Required: []string{"title"},
	}

	cfg, err := buildGenerateConfig(GenerateOptions{
		ResponseMIMEType: "application/json",
		ResponseSchema:   schema,
	})
	if err != nil {
		t.Fatalf("buildGenerateConfig() error = %v", err)
	}

	// cfg.ResponseSchema は *genai.Schema。同一ポインタで比較できるのは別名だからです。
	if cfg.ResponseSchema != schema {
		t.Fatalf("ResponseSchema = %+v, want 渡したスキーマそのもの", cfg.ResponseSchema)
	}
	if cfg.ResponseSchema.Type != genai.TypeObject {
		t.Errorf("Type = %q, want %q", cfg.ResponseSchema.Type, genai.TypeObject)
	}
}

// TestReExportedConstantsMatchSDK は、再エクスポートした定数が SDK の値と一致することを
// 検証します。ずれると「genai を import せずに値を選べる」という前提が崩れます。
func TestReExportedConstantsMatchSDK(t *testing.T) {
	thinking := map[ThinkingLevel]genai.ThinkingLevel{
		ThinkingUnspecified: genai.ThinkingLevelUnspecified,
		ThinkingMinimal:     genai.ThinkingLevelMinimal,
		ThinkingLow:         genai.ThinkingLevelLow,
		ThinkingMedium:      genai.ThinkingLevelMedium,
		ThinkingHigh:        genai.ThinkingLevelHigh,
	}
	for got, want := range thinking {
		if got != want {
			t.Errorf("ThinkingLevel = %q, want %q", got, want)
		}
	}

	safety := map[SafetyThreshold]genai.HarmBlockThreshold{
		SafetyBlockNone:           genai.HarmBlockThresholdBlockNone,
		SafetyBlockLowAndAbove:    genai.HarmBlockThresholdBlockLowAndAbove,
		SafetyBlockMediumAndAbove: genai.HarmBlockThresholdBlockMediumAndAbove,
		SafetyBlockOnlyHigh:       genai.HarmBlockThresholdBlockOnlyHigh,
	}
	for got, want := range safety {
		if got != want {
			t.Errorf("SafetyThreshold = %q, want %q", got, want)
		}
	}

	types := map[SchemaType]genai.Type{
		TypeString:  genai.TypeString,
		TypeNumber:  genai.TypeNumber,
		TypeInteger: genai.TypeInteger,
		TypeBoolean: genai.TypeBoolean,
		TypeArray:   genai.TypeArray,
		TypeObject:  genai.TypeObject,
	}
	for got, want := range types {
		if got != want {
			t.Errorf("SchemaType = %q, want %q", got, want)
		}
	}
}
