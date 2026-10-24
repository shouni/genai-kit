package imagegen

import (
	"fmt"

	"github.com/shouni/genai-kit/gemini"
)

// Response は生成された画像データとそのメタデータです。
type Response struct {
	// Data は生成画像のバイト列です。OutputURIPrefix を指定した場合は空で、URI に
	// 書き込み先が入ります。
	Data []byte
	// URI は、OutputURIPrefix を指定したときに API が画像を書き込んだ gs:// URI です。
	// 指定しなければ空で、Data に画像が入ります。
	URI      string
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
		if attachment.IsEmpty() {
			continue
		}
		mimeType := attachment.MIMEType
		if mimeType == "" && attachment.URI != "" {
			// サーバーが型を添えなかった場合だけ、書き込み先の拡張子から補う。
			// 分からなければ空のまま返す（理由は mimeTypeByPath を参照）。
			mimeType = mimeTypeByPath(attachment.URI)
		}
		return &Response{
			Data:     attachment.Data,
			URI:      attachment.URI,
			MIMEType: mimeType,
			UsedSeed: seedOrZero(p.opts.Seed),
			Model:    p.model,
			Prompt:   p.prompt,
			Usage:    resp.Usage,
		}, nil
	}

	return nil, fmt.Errorf("%w: response contains no image", ErrNoImageData)
}
