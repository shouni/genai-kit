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

// TestGenerateSendsPromptAndAttachments は、公開の入口が genai の型に触れずに
// プロンプトと添付を SDK へ届けることを検証します。
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
		t.Fatalf("contents = %+v, want user ロール 1 件", fake.gotContents)
	}
	parts := fake.gotContents[0].Parts
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(parts))
	}
	if parts[0].Text != "review this" {
		t.Errorf("parts[0].Text = %q", parts[0].Text)
	}
	if parts[1].InlineData == nil || parts[1].InlineData.MIMEType != "audio/mpeg" {
		t.Errorf("parts[1] = %+v, want 音声の添付", parts[1])
	}
}

// TestGenerateAppliesGenerateOptions は、GenerateOptions が公開の入口を通って
// SDK へ届くことを検証します。構造化出力が genai を import せずに使える根拠です。
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
		t.Error("ResponseJsonSchema が渡っていません")
	}
}

// TestGenerateValidatesInput は、添付経由の入口が共通の検証を飛ばす抜け道に
// ならないことを検証します。
func TestGenerateValidatesInput(t *testing.T) {
	client := &Client{modelClient: &fakeModelClient{}}

	_, err := client.Generate(context.Background(), "", "prompt", nil, GenerateOptions{})
	if !errors.Is(err, ErrEmptyModelName) {
		t.Errorf("Generate() error = %v, want ErrEmptyModelName", err)
	}
}

func TestGenerateText(t *testing.T) {
	t.Run("プロンプトだけを送ること", func(t *testing.T) {
		fake := &fakeModelClient{}
		client := &Client{modelClient: fake}

		if _, err := client.GenerateText(context.Background(), "gemini-test", "hello"); err != nil {
			t.Fatalf("GenerateText() error = %v", err)
		}
		parts := fake.gotContents[0].Parts
		if len(parts) != 1 || parts[0].Text != "hello" {
			t.Errorf("parts = %+v, want テキスト 1 件", parts)
		}
	})

	t.Run("空のプロンプトは ErrEmptyPrompt", func(t *testing.T) {
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
		{"モデル名が空", "", []*genai.Part{{Text: "hello"}}, ErrEmptyModelName},
		{"パーツが空", "gemini-test", nil, ErrEmptyParts},
		{"nil パーツを含む", "gemini-test", []*genai.Part{nil}, ErrInvalidPart},
		{"正常系", "gemini-test", []*genai.Part{{Text: "hello"}}, nil},
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

// TestGenerateWrapsAPIError は、SDK からの失敗にモデル名の文脈が付くことを
// 検証します。原因は %w で辿れます。
func TestGenerateWrapsAPIError(t *testing.T) {
	sentinel := errors.New("quota exceeded")
	client := &Client{modelClient: &fakeModelClient{err: sentinel}}

	_, err := client.Generate(context.Background(), "gemini-test", "prompt", nil, GenerateOptions{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Generate() error = %v, want 原因が辿れること", err)
	}
}

// TestGenerateRequestTimeoutBoundsCall は、RequestTimeout が実行中の 1 回にも
// 掛かることを検証します。上限時間の経過はバブル内の仮想時計で進むため、
// 実運用に近い値のまま実時間を消費しません。
func TestGenerateRequestTimeoutBoundsCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := &Client{
			modelClient:    &slowModelClient{delay: time.Hour},
			requestTimeout: 30 * time.Second,
		}

		_, err := client.Generate(context.Background(), "gemini-test", "hello", nil, GenerateOptions{})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("RequestTimeout が効いていません: err = %v", err)
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
		t.Fatalf("SystemInstruction が適用されていません: %+v", got.SystemInstruction)
	}
	if got.ResponseMIMEType != "application/json" {
		t.Fatalf("ResponseMIMEType = %q, want application/json", got.ResponseMIMEType)
	}
	if got.ImageConfig == nil || got.ImageConfig.AspectRatio != "16:9" ||
		got.ImageConfig.ImageSize != "1K" ||
		got.ImageConfig.PersonGeneration != string(PersonGenerationAllowAll) {
		t.Fatalf("ImageConfig が適用されていません: %+v", got.ImageConfig)
	}
}

// TestBuildGenerateConfigSamplingParams は、ゼロ値が意味を持つ項目をポインタで
// 区別できていることを検証します。Temperature 0 は「未設定」ではなく「決定的」です。
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

// TestBuildGenerateConfigRejectsOutOfRangeSeed は、int32 に収まらないシードを
// 送信前に弾くことを検証します。
func TestBuildGenerateConfigRejectsOutOfRangeSeed(t *testing.T) {
	seed := int64(math.MaxInt32) + 1

	if _, err := buildGenerateConfig(GenerateOptions{Seed: &seed}); !errors.Is(err, ErrInvalidSeed) {
		t.Errorf("buildGenerateConfig() error = %v, want ErrInvalidSeed", err)
	}
}

func TestApplyResponseFormat(t *testing.T) {
	schema := &Schema{Type: TypeObject}
	jsonSchema := map[string]any{"type": "object"}

	t.Run("audio/* はモダリティを AUDIO にする", func(t *testing.T) {
		got := &genai.GenerateContentConfig{}
		applyResponseFormat(got, GenerateOptions{ResponseMIMEType: "audio/wav"})

		if got.ResponseMIMEType != "audio/wav" {
			t.Errorf("ResponseMIMEType = %q", got.ResponseMIMEType)
		}
		if len(got.ResponseModalities) != 1 || got.ResponseModalities[0] != "AUDIO" {
			t.Errorf("ResponseModalities = %v, want [AUDIO]", got.ResponseModalities)
		}
	})

	t.Run("image/* はモダリティを IMAGE にする", func(t *testing.T) {
		got := &genai.GenerateContentConfig{}
		applyResponseFormat(got, GenerateOptions{ResponseMIMEType: "image/png"})

		if len(got.ResponseModalities) != 1 || got.ResponseModalities[0] != "IMAGE" {
			t.Errorf("ResponseModalities = %v, want [IMAGE]", got.ResponseModalities)
		}
	})

	t.Run("application/json はモダリティを触らない", func(t *testing.T) {
		got := &genai.GenerateContentConfig{}
		applyResponseFormat(got, GenerateOptions{ResponseMIMEType: "application/json"})

		if len(got.ResponseModalities) != 0 {
			t.Errorf("ResponseModalities = %v, want 空", got.ResponseModalities)
		}
	})

	t.Run("ResponseSchema のみ", func(t *testing.T) {
		got := &genai.GenerateContentConfig{}
		applyResponseFormat(got, GenerateOptions{ResponseSchema: schema})

		if got.ResponseSchema == nil || got.ResponseJsonSchema != nil {
			t.Errorf("ResponseSchema だけが送られるべきです: schema=%v json=%v",
				got.ResponseSchema, got.ResponseJsonSchema)
		}
	})

	// 両方送るとどちらが効くか不定になるため、片方だけを送ります。
	t.Run("両方指定なら ResponseJSONSchema を優先すること", func(t *testing.T) {
		got := &genai.GenerateContentConfig{}
		applyResponseFormat(got, GenerateOptions{ResponseSchema: schema, ResponseJSONSchema: jsonSchema})

		if got.ResponseJsonSchema == nil {
			t.Error("ResponseJsonSchema が送られていません")
		}
		if got.ResponseSchema != nil {
			t.Error("ResponseSchema も送られています。片方だけにすべきです")
		}
	})
}

func TestApplyImageConfig(t *testing.T) {
	t.Run("画像パラメータが無ければ ImageConfig を作らない", func(t *testing.T) {
		got := &genai.GenerateContentConfig{}
		applyImageConfig(got, GenerateOptions{SystemPrompt: "x"})

		if got.ImageConfig != nil {
			t.Errorf("ImageConfig = %+v, want nil", got.ImageConfig)
		}
	})

	t.Run("モダリティが未設定なら IMAGE を補うこと", func(t *testing.T) {
		got := &genai.GenerateContentConfig{}
		applyImageConfig(got, GenerateOptions{AspectRatio: "16:9"})

		if len(got.ResponseModalities) != 1 || got.ResponseModalities[0] != "IMAGE" {
			t.Errorf("ResponseModalities = %v, want [IMAGE]", got.ResponseModalities)
		}
	})

	t.Run("既に設定されたモダリティは上書きしないこと", func(t *testing.T) {
		got := &genai.GenerateContentConfig{ResponseModalities: []string{"AUDIO"}}
		applyImageConfig(got, GenerateOptions{AspectRatio: "16:9"})

		if len(got.ResponseModalities) != 1 || got.ResponseModalities[0] != "AUDIO" {
			t.Errorf("ResponseModalities = %v, want [AUDIO] のまま", got.ResponseModalities)
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
			name:    "未指定なら nil（モデル既定の思考挙動を上書きしない）",
			opts:    GenerateOptions{},
			wantNil: true,
		},
		{
			name:       "ThinkingBudget のみ",
			opts:       GenerateOptions{ThinkingBudget: new(int32(0))},
			wantBudget: new(int32(0)),
		},
		{
			name:      "ThinkingLevel のみ",
			opts:      GenerateOptions{ThinkingLevel: ThinkingLow},
			wantLevel: ThinkingLow,
		},
		{
			name:      "両方指定なら ThinkingLevel を優先し Budget は送らない",
			opts:      GenerateOptions{ThinkingLevel: ThinkingHigh, ThinkingBudget: new(int32(4096))},
			wantLevel: ThinkingHigh,
		},
		{
			name:       "Unspecified は未指定扱い",
			opts:       GenerateOptions{ThinkingLevel: ThinkingUnspecified, ThinkingBudget: new(int32(128))},
			wantBudget: new(int32(128)),
		},
		{
			name: "IncludeThoughts だけでも設定を送る",
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

	t.Run("nil なら nil", func(t *testing.T) {
		got, err := seedToPtrInt32(nil)
		if err != nil || got != nil {
			t.Errorf("seedToPtrInt32(nil) = %v, %v, want nil, nil", got, err)
		}
	})

	t.Run("範囲内はそのまま変換すること", func(t *testing.T) {
		got, err := seedToPtrInt32(&valid)
		if err != nil {
			t.Fatalf("seedToPtrInt32() error = %v", err)
		}
		if got == nil || *got != 12345 {
			t.Errorf("seedToPtrInt32() = %v, want 12345", got)
		}
	})

	for name, seed := range map[string]*int64{"上限超え": &over, "下限割れ": &under} {
		t.Run(name+"は ErrInvalidSeed", func(t *testing.T) {
			if _, err := seedToPtrInt32(seed); !errors.Is(err, ErrInvalidSeed) {
				t.Errorf("seedToPtrInt32() error = %v, want ErrInvalidSeed", err)
			}
		})
	}
}
