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
	"google.golang.org/genai/interactions/retry"
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
	modelClient       modelClient
	videoClient       videoClient
	interactionClient interactionClient
	// interactionRetries は Interactions の呼び出しに渡すリトライ設定です。
	// Interactions は genai の HTTPRetryOptions を読まないため、Config から別に組み立てます。
	interactionRetries *retry.Config
	requestTimeout     time.Duration
	// backendName はエラー文に載せる経路名です（"Vertex AI" / "Gemini API"）。
	// 402 のような課金のエラーは経路で原因が違うため、取り違えないように書き分けます。
	backendName string
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
		modelClient:        genAIModelClient{models: client.Models},
		videoClient:        genAIVideoClient{models: client.Models, operations: client.Operations},
		interactionClient:  client.Interactions,
		interactionRetries: cfg.interactionRetryConfig(),
		requestTimeout:     cfg.RequestTimeout,
		backendName:        cfg.backendName(),
	}, nil
}

// requestContext は Config.RequestTimeout を適用した context を返します。
// 未設定（0）の場合は呼び出し元の context をそのまま使います。
func (c *Client) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return withRequestTimeout(ctx, c.requestTimeout)
}

// backend はエラー文に載せる経路名を返します。New を通らずに組み立てた Client（テスト）でも
// 文が崩れないよう、未設定なら既定の Vertex AI とします。
func (c *Client) backend() string {
	if c.backendName == "" {
		return "Vertex AI"
	}
	return c.backendName
}

// withRequestTimeout は timeout が正の値のときだけ期限を付けた context を返します。
func withRequestTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}
