package gemini

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"google.golang.org/genai"
	ia "google.golang.org/genai/interactions/models/interactions"
	"google.golang.org/genai/interactions/models/operations"
	"google.golang.org/genai/interactions/retry"
)

// InteractionsGenerator が Generator を満たすことをコンパイル時に保証します。
var _ Generator = (*InteractionsGenerator)(nil)

// InteractionsConfig は Client.Interactions の設定です。
type InteractionsConfig struct {
	// AudioMIMEType は、音声を返すモデルに要求する出力形式です（"audio/wav" など）。
	// 空なら形式を指定せず、モデルの既定（Lyria では MP3）になります。
	//
	// 呼び出しごとの GenerateOptions.ResponseMIMEType が "audio/" で始まる場合はそちらが
	// 優先されます。音声を返さないモデルに形式を指定すると API が拒否するため、これを
	// 設定した InteractionsGenerator は音声生成専用にしてください。
	AudioMIMEType string
}

// InteractionsGenerator は、Interactions API で生成する Generator です。
//
// generateContent には音声の出力形式を指定する口が無く、Lyria は MP3 しか返せません。
// Interactions は response_format で形式を選べるため、音声生成だけをこちらへ逃がす用途を
// 想定しています（lyria.WithAudioGenerator に渡します）。作詞・作曲のようなテキスト生成は
// 従来の Client.Generate のままで構いません。
//
// Interactions の生成パラメータは generateContent より少なく、写せない GenerateOptions
// （サンプリング・思考・画像設定・構造化出力など）が指定された場合は ErrUnsupportedOption を
// 返します。黙って落とすと、指定したつもりの設定が効かないまま生成が課金されるためです。
type InteractionsGenerator struct {
	client         interactionClient
	retries        *retry.Config
	requestTimeout time.Duration
	audioMIMEType  string
}

// Interactions は、このクライアントの認証・リトライ・タイムアウト設定を引き継いだ
// InteractionsGenerator を返します。
func (c *Client) Interactions(cfg InteractionsConfig) *InteractionsGenerator {
	return &InteractionsGenerator{
		client:         c.interactionClient,
		retries:        c.interactionRetries,
		requestTimeout: c.requestTimeout,
		audioMIMEType:  cfg.AudioMIMEType,
	}
}

// Generate は、テキストプロンプトと添付から Interactions API で 1 回生成します。
func (g *InteractionsGenerator) Generate(ctx context.Context, model string, prompt string, attachments []Attachment, opts GenerateOptions) (*Response, error) {
	if model == "" {
		return nil, ErrEmptyModelName
	}
	body, err := buildModelInteraction(model, prompt, attachments, opts, g.audioMIMEType)
	if err != nil {
		return nil, err
	}

	ctx, cancel := withRequestTimeout(ctx, g.requestTimeout)
	defer cancel()

	var callOpts []operations.Option
	if g.retries != nil {
		callOpts = append(callOpts, operations.WithRetries(*g.retries))
	}
	resp, err := g.client.Create(ctx, operations.CreateInteractionRequest{
		Body: operations.NewCreateInteractionRequestBody(body),
	}, callOpts...)
	if err != nil {
		return nil, fmt.Errorf("モデル %s の Interactions 呼び出しに失敗しました: %w", model, err)
	}
	if resp == nil || resp.Interaction == nil {
		return nil, &APIResponseError{Reason: ErrEmptyResponse, Message: "Interactions のレスポンスが空でした"}
	}
	return responseFromInteraction(resp.Interaction)
}

// buildModelInteraction は Generate の引数を Interactions のリクエスト本文へ写します。
func buildModelInteraction(model, prompt string, attachments []Attachment, opts GenerateOptions, defaultAudioMIMEType string) (ia.CreateModelInteraction, error) {
	if err := checkInteractionOptions(opts); err != nil {
		return ia.CreateModelInteraction{}, err
	}
	input, err := interactionInput(prompt, attachments)
	if err != nil {
		return ia.CreateModelInteraction{}, err
	}
	genConfig, err := interactionGenerationConfig(opts)
	if err != nil {
		return ia.CreateModelInteraction{}, err
	}

	body := ia.CreateModelInteraction{
		Model:            ia.Model(model),
		Input:            &input,
		GenerationConfig: genConfig,
		SafetySettings:   interactionSafetySettings(opts.SafetySettings),
	}
	if opts.SystemPrompt != "" {
		body.SystemInstruction = new(opts.SystemPrompt)
	}

	audioMIMEType := defaultAudioMIMEType
	if strings.HasPrefix(opts.ResponseMIMEType, "audio/") {
		audioMIMEType = opts.ResponseMIMEType
	}
	if audioMIMEType != "" {
		format := ia.NewResponseFormat(ia.AudioResponseFormat{
			MimeType: new(ia.AudioResponseFormatMimeType(audioMIMEType)),
		})
		body.ResponseFormat = new(ia.NewCreateModelInteractionResponseFormat(format))
	}
	return body, nil
}

// checkInteractionOptions は、Interactions に写せない GenerateOptions を弾きます。
func checkInteractionOptions(opts GenerateOptions) error {
	var unsupported []string
	// Temperature と TopP は Interactions の generation_config にあるものの、SDK で
	// 非推奨になっているため写しません。
	if opts.Temperature != nil || opts.TopP != nil || opts.TopK != nil {
		unsupported = append(unsupported, "Temperature/TopP/TopK")
	}
	if opts.ThinkingBudget != nil || opts.ThinkingLevel != "" || opts.IncludeThoughts {
		unsupported = append(unsupported, "Thinking")
	}
	if opts.HasImageConfig() {
		unsupported = append(unsupported, "AspectRatio/ImageSize/PersonGeneration")
	}
	if opts.ResponseSchema != nil || opts.ResponseJSONSchema != nil {
		unsupported = append(unsupported, "ResponseSchema")
	}
	if opts.ResponseMIMEType != "" && !strings.HasPrefix(opts.ResponseMIMEType, "audio/") {
		unsupported = append(unsupported, "ResponseMIMEType="+opts.ResponseMIMEType)
	}
	if len(unsupported) > 0 {
		return fmt.Errorf("%w: %s", ErrUnsupportedOption, strings.Join(unsupported, ", "))
	}
	return nil
}

// interactionInput はプロンプトと添付を Interactions の入力へ写します。
//
// 添付の扱いは attachmentParts に揃えています（空の添付は落とす、Data と URI は排他、
// インラインデータには MIME type が必須）。違いは URI にも MIME type が要ることで、
// Interactions は内容ブロックを種類（画像・音声）ごとに型で分けるため、種類が分からない
// 参照は組み立てられません。テキストだけの場合は文字列で送ります（公式の例と同じ形）。
func interactionInput(prompt string, attachments []Attachment) (ia.InteractionsInput, error) {
	contents := make([]ia.Content, 0, len(attachments)+1)
	if prompt != "" {
		contents = append(contents, ia.NewContent(ia.TextContent{Text: prompt}))
	}

	for i, attachment := range attachments {
		if attachment.IsEmpty() {
			continue
		}
		field := fmt.Sprintf("attachments[%d]", i)
		if err := attachment.validateSource(ErrInvalidAttachment, field); err != nil {
			return ia.InteractionsInput{}, err
		}
		content, err := interactionContent(attachment, field)
		if err != nil {
			return ia.InteractionsInput{}, err
		}
		contents = append(contents, content)
	}

	switch {
	case len(contents) == 0:
		return ia.InteractionsInput{}, ErrEmptyParts
	case len(contents) == 1 && prompt != "":
		return ia.NewInteractionsInput(prompt), nil
	default:
		return ia.NewInteractionsInput(contents), nil
	}
}

// interactionContent は添付 1 件を、MIME type に応じた内容ブロックへ写します。
func interactionContent(attachment Attachment, field string) (ia.Content, error) {
	var data, uri *string
	if attachment.URI != "" {
		uri = new(attachment.URI)
	} else {
		data = new(base64.StdEncoding.EncodeToString(attachment.Data))
	}

	switch {
	case attachment.MIMEType == "":
		return ia.Content{}, fmt.Errorf("%w: %s にMIME typeが設定されていません", ErrInvalidAttachment, field)
	case strings.HasPrefix(attachment.MIMEType, "image/"):
		return ia.NewContent(ia.ImageContent{
			Data:     data,
			URI:      uri,
			MimeType: new(ia.ImageContentMimeType(attachment.MIMEType)),
		}), nil
	case strings.HasPrefix(attachment.MIMEType, "audio/"):
		return ia.NewContent(ia.AudioContent{
			Data:     data,
			URI:      uri,
			MimeType: new(ia.AudioContentMimeType(attachment.MIMEType)),
		}), nil
	default:
		return ia.Content{}, fmt.Errorf("%w: %s の MIME type %q は Interactions では送れません（画像と音声のみ）", ErrInvalidAttachment, field, attachment.MIMEType)
	}
}

// interactionGenerationConfig は、Interactions の generation_config にあたる項目を写します。
// 何も指定されていなければ nil を返し、モデルの既定に委ねます。
func interactionGenerationConfig(opts GenerateOptions) (*ia.GenerationConfig, error) {
	cfg := &ia.GenerationConfig{StopSequences: opts.StopSequences}
	if opts.MaxOutputTokens > 0 {
		cfg.MaxOutputTokens = new(int(opts.MaxOutputTokens))
	}
	if opts.Seed != nil {
		seed, err := seedToPtrInt32(opts.Seed)
		if err != nil {
			return nil, err
		}
		cfg.Seed = new(int(*seed))
	}

	if len(cfg.StopSequences) == 0 && cfg.MaxOutputTokens == nil && cfg.Seed == nil {
		return nil, nil
	}
	return cfg, nil
}

// interactionSafetySettings は genai の安全設定を Interactions の表記へ写します。
//
// 同じ設定でも表記が違います。genai は "HARM_CATEGORY_HATE_SPEECH" / "BLOCK_ONLY_HIGH"、
// Interactions は "hate_speech" / "block_only_high" です。NewSafetySettings で組み立てた値を
// そのまま渡せるよう、ここで変換します。
//
// BLOCK_NONE だけは "off" へ写します。SDK の列挙には block_none がありますが、API は
// どのカテゴリでも "The value 'block_none' is not supported" で拒否します（2026-10-18 に
// 実機で確認）。ブロックしないという意図に当たるのは off です。
func interactionSafetySettings(settings []*genai.SafetySetting) []ia.SafetySetting {
	if len(settings) == 0 {
		return nil
	}
	converted := make([]ia.SafetySetting, 0, len(settings))
	for _, setting := range settings {
		if setting == nil {
			continue
		}
		threshold := ia.Threshold(strings.ToLower(string(setting.Threshold)))
		if setting.Threshold == genai.HarmBlockThresholdBlockNone {
			threshold = ia.ThresholdOff
		}
		converted = append(converted, ia.SafetySetting{
			Type:      ia.HarmCategory(strings.ToLower(strings.TrimPrefix(string(setting.Category), "HARM_CATEGORY_"))),
			Threshold: threshold,
		})
	}
	return converted
}

// responseFromInteraction は Interactions の結果をパッケージ公開型の Response に変換します。
//
// 出力は steps の model_output に内容ブロックとして並びます。Lyria は譜面テキストと音声を
// 別のステップで返すため、全ステップを順に見てテキストは連結し、データは Attachments に
// 返却順で積みます。
func responseFromInteraction(it *ia.Interaction) (*Response, error) {
	if it.Status != ia.InteractionStatusCompleted {
		return nil, interactionStatusError(it)
	}

	var text strings.Builder
	var attachments []Attachment
	for _, step := range it.Steps {
		output := step.ModelOutputStep
		if output == nil {
			continue
		}
		if output.Error != nil {
			return nil, &APIResponseError{
				Reason:  ErrEmptyResponse,
				Message: "Interactions の出力にエラーが含まれていました: " + statusMessage(output.Error),
			}
		}
		for _, content := range output.Content {
			switch {
			case content.TextContent != nil:
				text.WriteString(content.TextContent.Text)
			case content.AudioContent != nil:
				attachment, err := decodeInteractionMedia(content.AudioContent.Data, content.AudioContent.URI, (*string)(content.AudioContent.MimeType))
				if err != nil {
					return nil, err
				}
				attachments = append(attachments, attachment)
			case content.ImageContent != nil:
				attachment, err := decodeInteractionMedia(content.ImageContent.Data, content.ImageContent.URI, (*string)(content.ImageContent.MimeType))
				if err != nil {
					return nil, err
				}
				attachments = append(attachments, attachment)
			}
		}
	}

	if text.Len() == 0 && len(attachments) == 0 {
		return nil, &APIResponseError{Reason: ErrEmptyResponse, Message: "Interactions のレスポンスに出力がありませんでした"}
	}

	resp := &Response{
		Text:        text.String(),
		Attachments: attachments,
		Usage:       tokenUsageFromInteraction(it.Usage),
	}
	for _, attachment := range attachments {
		switch {
		case len(attachment.Data) == 0:
		case strings.HasPrefix(attachment.MIMEType, "image/"):
			resp.Images = append(resp.Images, attachment.Data)
		case strings.HasPrefix(attachment.MIMEType, "audio/"):
			resp.Audios = append(resp.Audios, attachment.Data)
		}
	}
	return resp, nil
}

// decodeInteractionMedia は内容ブロックのデータ（base64）を Attachment に戻します。
// URI で返された場合はダウンロードせず、URI のまま渡します。
func decodeInteractionMedia(data, uri, mimeType *string) (Attachment, error) {
	attachment := Attachment{}
	if mimeType != nil {
		attachment.MIMEType = *mimeType
	}
	switch {
	case data != nil && *data != "":
		decoded, err := base64.StdEncoding.DecodeString(*data)
		if err != nil {
			return Attachment{}, fmt.Errorf("%w: Interactions の出力データを復号できませんでした: %w", ErrEmptyResponse, err)
		}
		attachment.Data = decoded
	case uri != nil:
		attachment.URI = *uri
	}
	return attachment, nil
}

// interactionStatusError は、completed 以外で返った interaction をエラーに分類します。
//
// incomplete と budget_exceeded は上限で打ち切られた途中結果なので ErrTruncated、それ以外
// （failed・cancelled・未完了のまま返った in_progress など）は使える出力が無いので
// ErrEmptyResponse です。
func interactionStatusError(it *ia.Interaction) *APIResponseError {
	reason := ErrEmptyResponse
	switch it.Status {
	case ia.InteractionStatusIncomplete, ia.InteractionStatusBudgetExceeded:
		reason = ErrTruncated
	}

	message := fmt.Sprintf("Interactions の生成が完了しませんでした（status: %s）", it.Status)
	var details []string
	for _, e := range it.Errors {
		if e.Message != nil && *e.Message != "" {
			details = append(details, *e.Message)
		}
	}
	if len(details) > 0 {
		message += ": " + strings.Join(details, "; ")
	}
	return &APIResponseError{Reason: reason, Message: message}
}

// statusMessage は Status の説明文を返します。メッセージが無ければコードで代用します。
func statusMessage(s *ia.Status) string {
	if s.Message != nil && *s.Message != "" {
		return *s.Message
	}
	if s.Code != nil {
		return fmt.Sprintf("code %d", *s.Code)
	}
	return "詳細なし"
}

// tokenUsageFromInteraction は Interactions の使用量を TokenUsage に写します。
func tokenUsageFromInteraction(u *ia.Usage) *TokenUsage {
	if u == nil {
		return nil
	}
	return &TokenUsage{
		PromptTokenCount:     int32FromPtr(u.TotalInputTokens),
		CandidatesTokenCount: int32FromPtr(u.TotalOutputTokens),
		TotalTokenCount:      int32FromPtr(u.TotalTokens),
		ThoughtsTokenCount:   int32FromPtr(u.TotalThoughtTokens),
	}
}

func int32FromPtr(v *int) int32 {
	if v == nil {
		return 0
	}
	return int32(*v)
}
