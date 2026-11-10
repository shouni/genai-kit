package gemini

import (
	"context"
	"errors"
	"math"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/genai"
)

// TestGenerateSendsPromptAndAttachments checks that the public entry point delivers the prompt
// and attachments to the SDK without touching genai types.
func TestGenerateSendsPromptAndAttachments(t *testing.T) {
	fake := &fakeModelClient{}
	client := &Client{modelClient: fake}

	resp, err := client.Generate(context.Background(), "gemini-test", "review this",
		[]Attachment{{MIMEType: "audio/mpeg", Data: []byte("song")}}, GenerateOptions{})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if resp.Text != "ok" {
		t.Errorf("resp.Text = %q, want %q", resp.Text, "ok")
	}

	if fake.gotModel != "gemini-test" {
		t.Errorf("model = %q", fake.gotModel)
	}
	if len(fake.gotContents) != 1 || fake.gotContents[0].Role != "user" {
		t.Fatalf("contents = %+v, want one user-role entry", fake.gotContents)
	}
	parts := fake.gotContents[0].Parts
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(parts))
	}
	if parts[0].Text != "review this" {
		t.Errorf("parts[0].Text = %q", parts[0].Text)
	}
	if parts[1].InlineData == nil || parts[1].InlineData.MIMEType != "audio/mpeg" {
		t.Errorf("parts[1] = %+v, want the audio attachment", parts[1])
	}
}

// TestGenerateAppliesGenerateOptions checks that GenerateOptions reach the SDK through the public
// entry point. This is what lets structured output be used without importing genai.
func TestGenerateAppliesGenerateOptions(t *testing.T) {
	fake := &fakeModelClient{}
	client := &Client{modelClient: fake}

	_, err := client.Generate(context.Background(), "gemini-test", "prompt", nil, GenerateOptions{
		ResponseMIMEType:   "application/json",
		ResponseJSONSchema: map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if fake.gotConfig.ResponseMIMEType != "application/json" {
		t.Errorf("ResponseMIMEType = %q", fake.gotConfig.ResponseMIMEType)
	}
	if fake.gotConfig.ResponseJsonSchema == nil {
		t.Error("ResponseJsonSchema was not passed")
	}
}

// TestGenerateValidatesInput checks that the attachment entry point does not become a loophole
// that skips the shared validation.
func TestGenerateValidatesInput(t *testing.T) {
	client := &Client{modelClient: &fakeModelClient{}}

	_, err := client.Generate(context.Background(), "", "prompt", nil, GenerateOptions{})
	if !errors.Is(err, ErrEmptyModelName) {
		t.Errorf("Generate() error = %v, want ErrEmptyModelName", err)
	}
}

func TestGenerateText(t *testing.T) {
	t.Run("sends only the prompt", func(t *testing.T) {
		fake := &fakeModelClient{}
		client := &Client{modelClient: fake}

		if _, err := client.GenerateText(context.Background(), "gemini-test", "hello"); err != nil {
			t.Fatalf("GenerateText() error = %v", err)
		}
		parts := fake.gotContents[0].Parts
		if len(parts) != 1 || parts[0].Text != "hello" {
			t.Errorf("parts = %+v, want one text part", parts)
		}
	})

	t.Run("an empty prompt is ErrEmptyPrompt", func(t *testing.T) {
		client := &Client{modelClient: &fakeModelClient{}}

		if _, err := client.GenerateText(context.Background(), "gemini-test", ""); !errors.Is(err, ErrEmptyPrompt) {
			t.Errorf("GenerateText() error = %v, want ErrEmptyPrompt", err)
		}
	})
}

func TestValidateGenerateInput(t *testing.T) {
	tests := []struct {
		name  string
		model string
		parts []*genai.Part
		want  error
	}{
		{"empty model name", "", []*genai.Part{{Text: "hello"}}, ErrEmptyModelName},
		{"empty parts", "gemini-test", nil, ErrEmptyParts},
		{"contains a nil part", "gemini-test", []*genai.Part{nil}, ErrInvalidPart},
		{"valid", "gemini-test", []*genai.Part{{Text: "hello"}}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateGenerateInput(tt.model, tt.parts)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("validateGenerateInput() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Errorf("validateGenerateInput() error = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestGenerateWrapsAPIError checks that a failure from the SDK gets the model name as context.
// The cause can be reached through %w.
func TestGenerateWrapsAPIError(t *testing.T) {
	sentinel := errors.New("quota exceeded")
	client := &Client{modelClient: &fakeModelClient{err: sentinel}}

	_, err := client.Generate(context.Background(), "gemini-test", "prompt", nil, GenerateOptions{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Generate() error = %v, want the cause to be reachable", err)
	}
}

// TestGenerateRequestTimeoutBoundsCall checks that RequestTimeout also applies to the in-flight
// call. The time limit elapses on the bubble's virtual clock, so values close to production
// consume no real time.
func TestGenerateRequestTimeoutBoundsCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := &Client{
			modelClient:    &slowModelClient{delay: time.Hour},
			requestTimeout: 30 * time.Second,
		}

		_, err := client.Generate(context.Background(), "gemini-test", "hello", nil, GenerateOptions{})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("RequestTimeout is not applied: err = %v", err)
		}
	})
}

func TestBuildGenerateConfigAppliesOptions(t *testing.T) {
	seed := int64(12345)

	got, err := buildGenerateConfig(GenerateOptions{
		SystemPrompt:     "system",
		ResponseMIMEType: "application/json",
		AspectRatio:      "16:9",
		ImageSize:        "1K",
		Seed:             &seed,
		PersonGeneration: PersonGenerationAllowAll,
	})
	if err != nil {
		t.Fatalf("buildGenerateConfig() error = %v", err)
	}

	if got.Seed == nil || *got.Seed != int32(seed) {
		t.Fatalf("Seed = %v, want %v", got.Seed, seed)
	}
	if got.SystemInstruction == nil || len(got.SystemInstruction.Parts) != 1 ||
		got.SystemInstruction.Parts[0].Text != "system" {
		t.Fatalf("SystemInstruction is not applied: %+v", got.SystemInstruction)
	}
	if got.ResponseMIMEType != "application/json" {
		t.Fatalf("ResponseMIMEType = %q, want application/json", got.ResponseMIMEType)
	}
	if got.ImageConfig == nil || got.ImageConfig.AspectRatio != "16:9" ||
		got.ImageConfig.ImageSize != "1K" ||
		got.ImageConfig.PersonGeneration != string(PersonGenerationAllowAll) {
		t.Fatalf("ImageConfig is not applied: %+v", got.ImageConfig)
	}
}

func TestBuildGenerateConfigOutputURIPrefix(t *testing.T) {
	t.Run("not sent when unspecified", func(t *testing.T) {
		got, err := buildGenerateConfig(GenerateOptions{})
		if err != nil {
			t.Fatalf("buildGenerateConfig() error = %v", err)
		}
		if got.GenerationOutputConfig != nil {
			t.Errorf("GenerationOutputConfig = %+v, want nil", got.GenerationOutputConfig)
		}
	})

	t.Run("gs:// is sent as the destination", func(t *testing.T) {
		got, err := buildGenerateConfig(GenerateOptions{OutputURIPrefix: "gs://bucket/prefix/"})
		if err != nil {
			t.Fatalf("buildGenerateConfig() error = %v", err)
		}
		if got.GenerationOutputConfig == nil || got.GenerationOutputConfig.OutputURIPrefix != "gs://bucket/prefix/" {
			t.Errorf("GenerationOutputConfig = %+v", got.GenerationOutputConfig)
		}
	})

	for _, prefix := range []string{"https://example.com/out/", "bucket/prefix/"} {
		t.Run("anything other than gs:// is refused before sending: "+prefix, func(t *testing.T) {
			_, err := buildGenerateConfig(GenerateOptions{OutputURIPrefix: prefix})
			if !errors.Is(err, ErrInvalidOutputURIPrefix) {
				t.Errorf("err = %v, want ErrInvalidOutputURIPrefix", err)
			}
		})
	}
}

// TestGenerateReadsFileDataOnlyWithOutputURIPrefix checks, through Generate, that FileData URIs
// are returned only for calls that specified OutputURIPrefix. The result of calls without it
// must be the same as before this feature was added.
func TestGenerateReadsFileDataOnlyWithOutputURIPrefix(t *testing.T) {
	tests := []struct {
		name string
		opts GenerateOptions
		want int
	}{
		{"not picked up when unspecified", GenerateOptions{}, 0},
		{"picked up when specified", GenerateOptions{OutputURIPrefix: "gs://bucket/out/"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeModelClient{resp: respWithParts(genai.FinishReasonStop,
				&genai.Part{FileData: &genai.FileData{FileURI: "gs://bucket/out/1.png", MIMEType: "image/png"}},
			)}
			client := &Client{modelClient: fake}

			resp, err := client.Generate(context.Background(), "gemini-test", "draw", nil, tt.opts)
			if err != nil {
				t.Fatalf("Generate() error = %v", err)
			}
			if len(resp.Attachments) != tt.want {
				t.Errorf("Attachments = %+v, want %d entries", resp.Attachments, tt.want)
			}
		})
	}
}

// TestBuildGenerateConfigSamplingParams checks that fields whose zero value is meaningful are
// distinguished with pointers. Temperature 0 means "deterministic", not "unset".
func TestBuildGenerateConfigSamplingParams(t *testing.T) {
	got, err := buildGenerateConfig(GenerateOptions{
		Temperature:     new(float32(0)),
		TopP:            new(float32(0.9)),
		TopK:            new(float32(40)),
		MaxOutputTokens: 2048,
		StopSequences:   []string{"END"},
	})
	if err != nil {
		t.Fatalf("buildGenerateConfig() error = %v", err)
	}

	if got.Temperature == nil || *got.Temperature != 0 {
		t.Errorf("Temperature = %v, want 0", got.Temperature)
	}
	if got.TopP == nil || *got.TopP != 0.9 {
		t.Errorf("TopP = %v, want 0.9", got.TopP)
	}
	if got.TopK == nil || *got.TopK != 40 {
		t.Errorf("TopK = %v, want 40", got.TopK)
	}
	if got.MaxOutputTokens != 2048 {
		t.Errorf("MaxOutputTokens = %d, want 2048", got.MaxOutputTokens)
	}
	if len(got.StopSequences) != 1 || got.StopSequences[0] != "END" {
		t.Errorf("StopSequences = %v", got.StopSequences)
	}
}

// TestBuildGenerateConfigRejectsOutOfRangeSeed checks that a seed that does not fit in int32
// is refused before sending.
func TestBuildGenerateConfigRejectsOutOfRangeSeed(t *testing.T) {
	seed := int64(math.MaxInt32) + 1

	if _, err := buildGenerateConfig(GenerateOptions{Seed: &seed}); !errors.Is(err, ErrInvalidSeed) {
		t.Errorf("buildGenerateConfig() error = %v, want ErrInvalidSeed", err)
	}
}

func TestApplyResponseFormat(t *testing.T) {
	schema := &Schema{Type: TypeObject}
	jsonSchema := map[string]any{"type": "object"}

	t.Run("audio/* sets the modality to AUDIO", func(t *testing.T) {
		got := &genai.GenerateContentConfig{}
		applyResponseFormat(got, GenerateOptions{ResponseMIMEType: "audio/wav"})

		if got.ResponseMIMEType != "audio/wav" {
			t.Errorf("ResponseMIMEType = %q", got.ResponseMIMEType)
		}
		if len(got.ResponseModalities) != 1 || got.ResponseModalities[0] != "AUDIO" {
			t.Errorf("ResponseModalities = %v, want [AUDIO]", got.ResponseModalities)
		}
	})

	t.Run("image/* sets the modality to IMAGE", func(t *testing.T) {
		got := &genai.GenerateContentConfig{}
		applyResponseFormat(got, GenerateOptions{ResponseMIMEType: "image/png"})

		if len(got.ResponseModalities) != 1 || got.ResponseModalities[0] != "IMAGE" {
			t.Errorf("ResponseModalities = %v, want [IMAGE]", got.ResponseModalities)
		}
	})

	t.Run("application/json leaves the modality alone", func(t *testing.T) {
		got := &genai.GenerateContentConfig{}
		applyResponseFormat(got, GenerateOptions{ResponseMIMEType: "application/json"})

		if len(got.ResponseModalities) != 0 {
			t.Errorf("ResponseModalities = %v, want empty", got.ResponseModalities)
		}
	})

	t.Run("ResponseSchema only", func(t *testing.T) {
		got := &genai.GenerateContentConfig{}
		applyResponseFormat(got, GenerateOptions{ResponseSchema: schema})

		if got.ResponseSchema == nil || got.ResponseJsonSchema != nil {
			t.Errorf("only ResponseSchema should be sent: schema=%v json=%v",
				got.ResponseSchema, got.ResponseJsonSchema)
		}
	})

	// Sending both leaves it undefined which one takes effect, so only one is sent.
	t.Run("prefers ResponseJSONSchema when both are specified", func(t *testing.T) {
		got := &genai.GenerateContentConfig{}
		applyResponseFormat(got, GenerateOptions{ResponseSchema: schema, ResponseJSONSchema: jsonSchema})

		if got.ResponseJsonSchema == nil {
			t.Error("ResponseJsonSchema was not sent")
		}
		if got.ResponseSchema != nil {
			t.Error("ResponseSchema was sent too; only one should be")
		}
	})
}

func TestApplyImageConfig(t *testing.T) {
	t.Run("does not create ImageConfig without image parameters", func(t *testing.T) {
		got := &genai.GenerateContentConfig{}
		applyImageConfig(got, GenerateOptions{SystemPrompt: "x"})

		if got.ImageConfig != nil {
			t.Errorf("ImageConfig = %+v, want nil", got.ImageConfig)
		}
	})

	t.Run("fills in IMAGE when the modality is unset", func(t *testing.T) {
		got := &genai.GenerateContentConfig{}
		applyImageConfig(got, GenerateOptions{AspectRatio: "16:9"})

		if len(got.ResponseModalities) != 1 || got.ResponseModalities[0] != "IMAGE" {
			t.Errorf("ResponseModalities = %v, want [IMAGE]", got.ResponseModalities)
		}
	})

	t.Run("does not overwrite an already set modality", func(t *testing.T) {
		got := &genai.GenerateContentConfig{ResponseModalities: []string{"AUDIO"}}
		applyImageConfig(got, GenerateOptions{AspectRatio: "16:9"})

		if len(got.ResponseModalities) != 1 || got.ResponseModalities[0] != "AUDIO" {
			t.Errorf("ResponseModalities = %v, want it to stay [AUDIO]", got.ResponseModalities)
		}
	})
}

func TestBuildThinkingConfig(t *testing.T) {
	tests := []struct {
		name       string
		opts       GenerateOptions
		wantNil    bool
		wantLevel  ThinkingLevel
		wantBudget *int32
	}{
		{
			name:    "nil when unspecified (does not override the model's default thinking behaviour)",
			opts:    GenerateOptions{},
			wantNil: true,
		},
		{
			name:       "ThinkingBudget only",
			opts:       GenerateOptions{ThinkingBudget: new(int32(0))},
			wantBudget: new(int32(0)),
		},
		{
			name:      "ThinkingLevel only",
			opts:      GenerateOptions{ThinkingLevel: ThinkingLow},
			wantLevel: ThinkingLow,
		},
		{
			name:      "prefers ThinkingLevel when both are set and does not send Budget",
			opts:      GenerateOptions{ThinkingLevel: ThinkingHigh, ThinkingBudget: new(int32(4096))},
			wantLevel: ThinkingHigh,
		},
		{
			name:       "Unspecified counts as unspecified",
			opts:       GenerateOptions{ThinkingLevel: ThinkingUnspecified, ThinkingBudget: new(int32(128))},
			wantBudget: new(int32(128)),
		},
		{
			name: "sends the config for IncludeThoughts alone",
			opts: GenerateOptions{IncludeThoughts: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildThinkingConfig(tt.opts)

			if tt.wantNil {
				if got != nil {
					t.Fatalf("buildThinkingConfig() = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("ThinkingConfig = nil")
			}
			if got.ThinkingLevel != tt.wantLevel {
				t.Errorf("ThinkingLevel = %q, want %q", got.ThinkingLevel, tt.wantLevel)
			}
			switch {
			case tt.wantBudget == nil && got.ThinkingBudget != nil:
				t.Errorf("ThinkingBudget = %v, want nil", *got.ThinkingBudget)
			case tt.wantBudget != nil && got.ThinkingBudget == nil:
				t.Errorf("ThinkingBudget = nil, want %v", *tt.wantBudget)
			case tt.wantBudget != nil && *got.ThinkingBudget != *tt.wantBudget:
				t.Errorf("ThinkingBudget = %v, want %v", *got.ThinkingBudget, *tt.wantBudget)
			}
			if got.IncludeThoughts != tt.opts.IncludeThoughts {
				t.Errorf("IncludeThoughts = %v, want %v", got.IncludeThoughts, tt.opts.IncludeThoughts)
			}
		})
	}
}

func TestSeedToPtrInt32(t *testing.T) {
	valid := int64(12345)
	over := int64(math.MaxInt32) + 1
	under := int64(math.MinInt32) - 1

	t.Run("nil gives nil", func(t *testing.T) {
		got, err := seedToPtrInt32(nil)
		if err != nil || got != nil {
			t.Errorf("seedToPtrInt32(nil) = %v, %v, want nil, nil", got, err)
		}
	})

	t.Run("converts in-range values as is", func(t *testing.T) {
		got, err := seedToPtrInt32(&valid)
		if err != nil {
			t.Fatalf("seedToPtrInt32() error = %v", err)
		}
		if got == nil || *got != 12345 {
			t.Errorf("seedToPtrInt32() = %v, want 12345", got)
		}
	})

	for name, seed := range map[string]*int64{"above the maximum": &over, "below the minimum": &under} {
		t.Run(name+" is ErrInvalidSeed", func(t *testing.T) {
			if _, err := seedToPtrInt32(seed); !errors.Is(err, ErrInvalidSeed) {
				t.Errorf("seedToPtrInt32() error = %v, want ErrInvalidSeed", err)
			}
		})
	}
}
