package gemini

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"google.golang.org/genai"
)

// Generator は、テキストとバイナリ添付からコンテンツを生成するインターフェースです。
//
// genai の型を含まないため、利用側はこのインターフェースにだけ依存すれば
// genai SDK を import せずに済みます。モックも 1 メソッドで書けます。
type Generator interface {
	Generate(ctx context.Context, model string, prompt string, attachments []Attachment, opts GenerateOptions) (*Response, error)
}

// GenerateText は、オプション無しの純粋なテキストプロンプトからコンテンツを生成する最短経路です。
func (c *Client) GenerateText(ctx context.Context, model string, prompt string) (*Response, error) {
	if prompt == "" {
		return nil, ErrEmptyPrompt
	}
	parts := []*genai.Part{{Text: prompt}}
	return c.generateParts(ctx, model, parts, GenerateOptions{})
}

// Generate は、テキストプロンプトとバイナリ添付からコンテンツを生成します。
//
// テキスト 1 つと添付 n 件という、マルチモーダル呼び出しのほとんどを占める形に
// 絞った、genai の型を伴わない生成の入口です。添付が無ければ attachments に nil を
// 渡します（「プロンプト + GenerateOptions」の入口になります）。
//
// prompt が空でも添付があれば送信します（音声や画像だけを渡して解析させる用途）。
// 両方が空の場合と、データを持つ添付に MIME type が無い場合はエラーを返します。
func (c *Client) Generate(ctx context.Context, model string, prompt string, attachments []Attachment, opts GenerateOptions) (*Response, error) {
	parts, err := attachmentParts(prompt, attachments)
	if err != nil {
		return nil, err
	}
	return c.generateParts(ctx, model, parts, opts)
}

// generateParts はマルチモーダルパーツからコンテンツを生成する共通経路です。
//
// 公開の入口は genai の型を伴わない GenerateText / Generate で、
// genai.Part を直接受けるこの関数は公開しません（SDK の型を公開面に漏らさないため）。
func (c *Client) generateParts(ctx context.Context, model string, parts []*genai.Part, opts GenerateOptions) (*Response, error) {
	if err := validateGenerateInput(model, parts); err != nil {
		return nil, err
	}

	contents := []*genai.Content{{Role: "user", Parts: parts}}

	genConfig, err := buildGenerateConfig(opts)
	if err != nil {
		return nil, err
	}

	return c.generate(ctx, model, contents, genConfig)
}

func validateGenerateInput(model string, parts []*genai.Part) error {
	if model == "" {
		return ErrEmptyModelName
	}
	if len(parts) == 0 {
		return ErrEmptyParts
	}
	if slices.Contains(parts, nil) {
		return ErrInvalidPart
	}
	return nil
}

// buildThinkingConfig は思考設定を組み立てます。
// 何も指定がなければ nil を返します。常に送るとモデル既定の思考挙動を上書きしてしまうためです。
//
// ThinkingLevel（段階指定）と ThinkingBudget（トークン数指定）は排他的な指定方法です。
// 両方が設定された場合は、モデル非依存で移植性の高い ThinkingLevel を優先します。
func buildThinkingConfig(opts GenerateOptions) *genai.ThinkingConfig {
	hasLevel := opts.ThinkingLevel != "" && opts.ThinkingLevel != genai.ThinkingLevelUnspecified
	if !hasLevel && opts.ThinkingBudget == nil && !opts.IncludeThoughts {
		return nil
	}

	cfg := &genai.ThinkingConfig{IncludeThoughts: opts.IncludeThoughts}
	if hasLevel {
		cfg.ThinkingLevel = opts.ThinkingLevel
		return cfg
	}
	cfg.ThinkingBudget = opts.ThinkingBudget
	return cfg
}

// applyResponseFormat は、レスポンスの MIME type・モダリティ・構造化出力スキーマを適用します。
func applyResponseFormat(genConfig *genai.GenerateContentConfig, opts GenerateOptions) {
	if opts.ResponseMIMEType != "" {
		genConfig.ResponseMIMEType = opts.ResponseMIMEType

		if strings.HasPrefix(opts.ResponseMIMEType, "audio/") {
			genConfig.ResponseModalities = []string{"AUDIO"}
		} else if strings.HasPrefix(opts.ResponseMIMEType, "image/") {
			genConfig.ResponseModalities = []string{"IMAGE"}
		}
	}
	// ResponseJSONSchema と ResponseSchema は排他。両方送るとどちらが効くか不定になるため、
	// 新しい ResponseJSONSchema を優先して片方だけ送る。
	switch {
	case opts.ResponseJSONSchema != nil:
		genConfig.ResponseJsonSchema = opts.ResponseJSONSchema
	case opts.ResponseSchema != nil:
		genConfig.ResponseSchema = opts.ResponseSchema
	}
}

// applyImageConfig は、画像生成 (Imagen/Nano Banana) 特有の設定を適用します。
func applyImageConfig(genConfig *genai.GenerateContentConfig, opts GenerateOptions) {
	if !opts.HasImageConfig() {
		return
	}
	genConfig.ImageConfig = &genai.ImageConfig{}

	if len(genConfig.ResponseModalities) == 0 {
		genConfig.ResponseModalities = []string{"IMAGE"}
	}

	if opts.AspectRatio != "" {
		genConfig.ImageConfig.AspectRatio = opts.AspectRatio
	}
	if opts.ImageSize != "" {
		genConfig.ImageConfig.ImageSize = opts.ImageSize
	}
	if opts.PersonGeneration != PersonGenerationUnspecified {
		genConfig.ImageConfig.PersonGeneration = string(opts.PersonGeneration)
	}
}

// buildGenerateConfig は GenerateOptions を genai の生成設定へ変換します。
// Client のメソッドにしないのは Client の状態に依存しないためで、
// クライアント無しでテストできます。
func buildGenerateConfig(opts GenerateOptions) (*genai.GenerateContentConfig, error) {
	genConfig := &genai.GenerateContentConfig{
		SafetySettings:  opts.SafetySettings,
		Temperature:     opts.Temperature,
		TopP:            opts.TopP,
		TopK:            opts.TopK,
		MaxOutputTokens: opts.MaxOutputTokens,
		StopSequences:   opts.StopSequences,
	}

	genConfig.ThinkingConfig = buildThinkingConfig(opts)
	applyResponseFormat(genConfig, opts)

	if opts.Seed != nil {
		seed, err := seedToPtrInt32(opts.Seed)
		if err != nil {
			return nil, err
		}
		genConfig.Seed = seed
	}
	if opts.SystemPrompt != "" {
		genConfig.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{{Text: opts.SystemPrompt}},
		}
	}
	applyImageConfig(genConfig, opts)

	return genConfig, nil
}

// generate は共通の API 呼び出しをカプセル化します。
// リトライは genai SDK が内蔵のものを行います（設定は Config.retryOptions）。
// Config.RequestTimeout が設定されている場合、リトライを含む呼び出し全体に適用されます
// （SDK のリトライループは ctx.Err() を見るため、締切の外へは出ません）。
func (c *Client) generate(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*Response, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()

	resp, err := c.modelClient.GenerateContent(ctx, model, contents, config)
	if err != nil {
		return nil, fmt.Errorf("モデル %s の Vertex AI 呼び出しに失敗しました: %w", model, err)
	}
	return responseFromGenAI(resp)
}

// seedToPtrInt32 は *int64 を SDK 用の *int32 に変換します。
func seedToPtrInt32(s *int64) (*int32, error) {
	if s == nil {
		return nil, nil
	}

	if *s > math.MaxInt32 || *s < math.MinInt32 {
		return nil, fmt.Errorf("%w (入力値: %d)", ErrInvalidSeed, *s)
	}

	return new(int32(*s)), nil
}
