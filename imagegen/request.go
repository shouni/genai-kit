package imagegen

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"

	"github.com/shouni/genai-kit/gemini"
)

// Request は画像生成 1 回分の要求です。
//
// Images が 1 枚なら参照付きの単発生成、複数なら参照画像を統合した融合生成、
// 空ならテキストのみの生成になります。枚数が解釈を決めるため、単発と融合で
// 型を分けません。
//
// gemini.GenerateOptions を埋め込んでいるため、SystemPrompt / AspectRatio /
// ImageSize / Seed / Temperature などの生成パラメータはフィールド昇格でそのまま
// 設定できます。フィールドを写し取ると gemini 側の追加のたびに 2 か所の同期が
// 必要になるため、埋め込みにしています。
//
// 昇格したフィールドのうち SafetySettings と PersonGeneration は、未指定の場合のみ
// 次の既定値が補われます。
//
//	SafetySettings   → gemini.NewSafetySettings(gemini.SafetyBlockNone)
//	PersonGeneration → gemini.PersonGenerationAllowAll
//
// 安全フィルタは Vertex AI が OFF を受け付けないため BLOCK_NONE、人物生成は
// キャラクター生成が主用途のため許可が既定です。明示した値は上書きしません。
// 無条件に上書きすると、利用側が安全フィルタを厳しくする手段が無くなるためです。
type Request struct {
	// Model は生成に使うモデル名です。必須。
	Model string
	// Prompt は生成指示です。NegativePrompt と合わせて空の場合はエラーです。
	Prompt string
	// NegativePrompt は生成に含めたくない要素です。API のフィールドではなく、
	// 区切り "\n\n[Negative Prompt]\n" を挟んで Prompt へ連結して送られます。
	// この見た目は互換性の契約です。下流のプロンプト実装がこの描画に依存しているため、
	// 区切りの文字列は変わりません。
	// Prompt と NegativePrompt の両方が（空白を除いて）空ならエラーです。
	NegativePrompt string
	// Images は参照画像の gs:// URI です。並び順はモデルの解釈に影響するため
	// 保持されます。空文字列の要素はエラーではなく、送信対象から黙って外れます
	// （「このキャラクターには参照画像が無い」を、呼び出し側が要素の欠落として
	// 表現できるようにするためです）。
	Images []string

	gemini.GenerateOptions
}

// preparedRequest は、検証と組み立てを終えて送信できる状態になったリクエストです。
type preparedRequest struct {
	model  string
	prompt string
	opts   gemini.GenerateOptions
}

// prepare はリクエストを検証し、送信用の形へ組み立てます。
func (c *Client) prepare(req Request) (preparedRequest, error) {
	if req.Model == "" {
		return preparedRequest{}, ErrModelRequired
	}
	finalPrompt := buildFinalPrompt(req.Prompt, req.NegativePrompt)
	if finalPrompt == "" {
		return preparedRequest{}, ErrEmptyPrompt
	}

	opts := req.GenerateOptions
	// シードを生成側で決めるのは送信前の1回だけ。toResponse は opts.Seed を
	// そのまま UsedSeed として返すため、ここで埋めた値が呼び出し側に届く。
	if c.autoSeed && opts.Seed == nil {
		opts.Seed = newSeed()
	}

	return preparedRequest{
		model:  req.Model,
		prompt: finalPrompt,
		opts:   applyDefaults(opts),
	}, nil
}

// negativePromptSeparator は、ネガティブプロンプトをプロンプト本文へ連結する際の区切りです。
//
// この描画は互換性の契約です。ネガティブプロンプトは API のフィールドではなく、
// 下流のプロンプト実装がこの区切りの見た目に依存しているため、変更しないでください。
const negativePromptSeparator = "\n\n[Negative Prompt]\n"

// buildFinalPrompt はプロンプトとネガティブプロンプトを結合します。
func buildFinalPrompt(prompt, negative string) string {
	p := strings.TrimSpace(prompt)
	n := strings.TrimSpace(negative)

	if p == "" && n == "" {
		return ""
	}
	if n == "" {
		return p
	}

	return p + negativePromptSeparator + n
}

// applyDefaults は、呼び出し側が未指定の項目に既定値を補います。
//
// 明示された SafetySettings / PersonGeneration は尊重します。無条件に上書きすると、
// 利用側が安全フィルタを厳しくする手段が無くなるためです。
func applyDefaults(opts gemini.GenerateOptions) gemini.GenerateOptions {
	if opts.SafetySettings == nil {
		// Vertex AI は OFF を受け付けないため BLOCK_NONE を使います。
		opts.SafetySettings = gemini.NewSafetySettings(gemini.SafetyBlockNone)
	}
	// キャラクター生成が主用途のため、未指定時は人物生成を許可する。
	if opts.PersonGeneration == gemini.PersonGenerationUnspecified {
		opts.PersonGeneration = gemini.PersonGenerationAllowAll
	}
	return opts
}

// attachmentsFor は参照画像の gs:// URI を送信用の添付へ変換します。
//
// Vertex AI は gs:// をモデル側で解決するため、バイト列の取得も転送も起きません。
// 変換は純粋な文字列処理なので、並び順を保ったまま素直に回します。
// MIME type は URI の拡張子から推測し、判別できない場合は設定しません
// （理由は mimeTypeByPath を参照）。
func attachmentsFor(uris []string) ([]gemini.Attachment, error) {
	attachments := make([]gemini.Attachment, 0, len(uris))
	for _, uri := range uris {
		// 参照先を持たない要素は送るものが無いので落とす（理由は Request.Images を参照）。
		if uri == "" {
			continue
		}
		if !isGCSURI(uri) {
			return nil, fmt.Errorf("%w: %q", ErrUnsupportedReference, uri)
		}
		attachments = append(attachments, gemini.Attachment{URI: uri, MIMEType: mimeTypeByPath(uri)})
	}
	return attachments, nil
}

// newSeed は自動シード採番用の乱数シードを返します。
//
// gemini が int32 の範囲外を弾く（ErrInvalidSeed）ため、範囲内に収めます。
func newSeed() *int64 {
	seed := rand.Int64N(math.MaxInt32)
	return &seed
}

// seedOrZero は *int64 を安全にデリファレンスします。
// nil（WithoutAutoSeed でシードも未指定）は 0 になります。
func seedOrZero(seed *int64) int64 {
	if seed == nil {
		return 0
	}
	return *seed
}
