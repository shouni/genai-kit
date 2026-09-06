// Package gemini は、Vertex AI 向けの genai SDK をラップし、
// リトライ設定とレスポンス抽出を備えたクライアントを提供します。
//
// バックエンドは Vertex AI です。認証は Application Default Credentials (ADC) に
// 従い、Config には ProjectID と LocationID を渡します。Config.APIKey を指定した
// 場合だけ Gemini API (Google AI Studio) へ切り替わりますが、これは Vertex AI に
// 無いモデルのための暫定的な例外です（Config.APIKey を参照）。
//
// 公開 API に genai の型は現れません。設定値の型と定数は別名として再エクスポート
// してあるため（ThinkingLevel / SafetyThreshold / Schema / SchemaType /
// VideoReferenceType）、値を選ぶためだけに genai SDK を import する必要はありません。
// 別名なので、genai の値をそのまま渡しても構いません。
package gemini

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/genai"
)

// Client がパッケージ公開インターフェースを満たすことをコンパイル時に保証します。
// これらのアサーションがないと、Client のメソッドシグネチャがドリフトしても
// 下流の利用側がビルドされるまで気付けません。
var (
	_ Generator      = (*Client)(nil)
	_ VideoGenerator = (*Client)(nil)
)

// Client は Gemini SDK をラップしたメイン構造体です。
type Client struct {
	modelClient    modelClient
	videoClient    videoClient
	requestTimeout time.Duration
}

// New は提供された設定に基づいて、新しいクライアントを作成します。
// バックエンドは Config が決めます（既定は Vertex AI）。
func New(ctx context.Context, cfg Config) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	clientCfg, err := cfg.toClientConfig()
	if err != nil {
		return nil, err
	}
	client, err := genai.NewClient(ctx, clientCfg)
	if err != nil {
		return nil, fmt.Errorf("gemini: クライアントの作成に失敗しました: %w", err)
	}

	return &Client{
		modelClient:    genAIModelClient{models: client.Models},
		videoClient:    genAIVideoClient{models: client.Models, operations: client.Operations},
		requestTimeout: cfg.RequestTimeout,
	}, nil
}

// requestContext は Config.RequestTimeout を適用した context を返します。
// 未設定（0）の場合は呼び出し元の context をそのまま使います。
func (c *Client) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.requestTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.requestTimeout)
}
