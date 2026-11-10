package gemini

import (
	"context"

	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/operations"
)

type modelClient interface {
	GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
}

// videoClient is the genai call surface used for video generation. genai splits starting a
// video (Models) and checking its progress (Operations) across separate receivers, but to the
// caller it is one flow, so they are combined into one interface here.
type videoClient interface {
	GenerateVideosFromSource(ctx context.Context, model string, source *genai.GenerateVideosSource, config *genai.GenerateVideosConfig) (*genai.GenerateVideosOperation, error)
	GetVideosOperation(ctx context.Context, operation *genai.GenerateVideosOperation, config *genai.GetOperationConfig) (*genai.GenerateVideosOperation, error)
}

// interactionClient is the call surface of the Interactions API. genai's *interactions.Interactions
// satisfies it as is.
type interactionClient interface {
	Create(ctx context.Context, request operations.CreateInteractionRequest, opts ...operations.Option) (*operations.CreateInteractionResponse, error)
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
