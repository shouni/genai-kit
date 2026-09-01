package imagegen

import (
	"fmt"

	"github.com/shouni/genai-kit/gemini"
)

// Response は生成された画像データとそのメタデータです。
type Response struct {
	Data     []byte
	MIMEType string
	// UsedSeed はリクエストで指定した（または自動採番された）シードです。
	// API はレスポンスにシードを返さないため、これは送信値の記録です。
	UsedSeed int64
	// Model は生成に使ったモデル名です。コスト集計やメタデータ保存のために、
	// 呼び出し側がリクエストから別途持ち回らずに済むよう応答へ含めます。
	Model string
	// Prompt は実際に送信した最終プロンプト（ネガティブプロンプト結合済み）です。
	Prompt string
	// Usage はトークン使用量です。モデルが返さない場合は nil です。
	Usage *gemini.TokenUsage
}

// toResponse はレスポンスから画像データを抽出します。
//
// FinishReason の検証（安全フィルタによるブロック等）は gemini が行い、ブロック時は
// 生成呼び出し自体がエラーを返すため、ここでは行いません。このパッケージが区別するのは
// 「画像データが無い」ことだけです。
func (p preparedRequest) toResponse(resp *gemini.Response) (*Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("%w: no response", ErrNoImageData)
	}

	// gemini.Response.Attachments は MIME type 込みで返るため、保存時の Content-Type を
	// 決めるために生の SDK レスポンスを辿る必要はありません。
	for _, attachment := range resp.Attachments {
		if len(attachment.Data) == 0 {
			continue
		}
		return &Response{
			Data:     attachment.Data,
			MIMEType: attachment.MIMEType,
			UsedSeed: seedOrZero(p.opts.Seed),
			Model:    p.model,
			Prompt:   p.prompt,
			Usage:    resp.Usage,
		}, nil
	}

	return nil, fmt.Errorf("%w: response contains no inline image", ErrNoImageData)
}
