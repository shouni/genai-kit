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
		{"nil receiver", nil, false},
		{"no settings", &GenerateOptions{}, false},
		{"with AspectRatio", &GenerateOptions{AspectRatio: "16:9"}, true},
		{"with ImageSize", &GenerateOptions{ImageSize: "1K"}, true},
		{"with PersonGeneration", &GenerateOptions{PersonGeneration: PersonGenerationAllowAll}, true},
		{"non-image settings only", &GenerateOptions{SystemPrompt: "test"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.opts.HasImageConfig(); got != tt.want {
				t.Errorf("HasImageConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestNewSafetySettings checks that the same threshold is set on all four standard categories.
// If even one is missing, that category runs at the API's default threshold, and the intended
// loosening or tightening only partly takes effect.
func TestNewSafetySettings(t *testing.T) {
	got := NewSafetySettings(SafetyBlockNone)

	want := []genai.HarmCategory{
		genai.HarmCategoryHarassment,
		genai.HarmCategoryHateSpeech,
		genai.HarmCategorySexuallyExplicit,
		genai.HarmCategoryDangerousContent,
	}
	if len(got) != len(want) {
		t.Fatalf("settings = %d entries, want %d entries", len(got), len(want))
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

// TestSchemaAliasReachesSDKUnchanged checks that a schema written with this package's alias
// reaches the SDK unchanged.
//
// The alias exists to keep downstream code from importing genai just for structured
// output. That only holds if it is "the same type", so the fact that this comparison
// compiles is itself the check.
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

	// cfg.ResponseSchema is *genai.Schema. It can be compared as the same pointer because it is an alias.
	if cfg.ResponseSchema != schema {
		t.Fatalf("ResponseSchema = %+v, want the schema that was passed in", cfg.ResponseSchema)
	}
	if cfg.ResponseSchema.Type != genai.TypeObject {
		t.Errorf("Type = %q, want %q", cfg.ResponseSchema.Type, genai.TypeObject)
	}
}

// TestReExportedConstantsMatchSDK checks that the re-exported constants match the SDK's values.
// If they drift, the premise that "a value can be picked without importing genai" breaks.
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
