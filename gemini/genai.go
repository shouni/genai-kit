package gemini

import (
	"context"

	"google.golang.org/genai"
)

type modelClient interface {
	GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
}

// videoClient は動画生成に使う genai の呼び出し面です。genai では動画の開始
// （Models）と進捗確認（Operations）が別レシーバに分かれていますが、利用側から見れば
// 1つの流れなので、ここでは1つのインターフェースにまとめています。
type videoClient interface {
	GenerateVideosFromSource(ctx context.Context, model string, source *genai.GenerateVideosSource, config *genai.GenerateVideosConfig) (*genai.GenerateVideosOperation, error)
	GetVideosOperation(ctx context.Context, operation *genai.GenerateVideosOperation, config *genai.GetOperationConfig) (*genai.GenerateVideosOperation, error)
}

type genAIModelClient struct {
	models *genai.Models
}

func (c genAIModelClient) GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	return c.models.GenerateContent(ctx, model, contents, config)
}

type genAIVideoClient struct {
	models     *genai.Models
	operations *genai.Operations
}

func (c genAIVideoClient) GenerateVideosFromSource(ctx context.Context, model string, source *genai.GenerateVideosSource, config *genai.GenerateVideosConfig) (*genai.GenerateVideosOperation, error) {
	return c.models.GenerateVideosFromSource(ctx, model, source, config)
}

func (c genAIVideoClient) GetVideosOperation(ctx context.Context, operation *genai.GenerateVideosOperation, config *genai.GetOperationConfig) (*genai.GenerateVideosOperation, error) {
	return c.operations.GetVideosOperation(ctx, operation, config)
}
