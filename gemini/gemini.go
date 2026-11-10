// Package gemini wraps the genai SDK for Vertex AI and provides a client
// with retry settings and response extraction.
//
// The backend is Vertex AI. Authentication follows Application Default Credentials (ADC),
// and Config takes ProjectID and LocationID. Only when Config.APIKey is set does it switch
// to the Gemini API (Google AI Studio), a temporary exception for models that are not on
// Vertex AI (see Config.APIKey).
//
// No genai types appear in the public API. Configuration value types and constants are
// re-exported as aliases (ThinkingLevel / SafetyThreshold / Schema / SchemaType /
// VideoReferenceType), so there is no need to import the genai SDK just to pick a value.
// Because they are aliases, genai values can also be passed as is.
package gemini

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/genai"
	"google.golang.org/genai/interactions/retry"
)

// These assertions guarantee at compile time that Client satisfies the package's public interfaces.
// Without them, a drifting Client method signature would go unnoticed until a downstream
// caller is built.
var (
	_ Generator      = (*Client)(nil)
	_ VideoGenerator = (*Client)(nil)
)

// Client is the main struct wrapping the Gemini SDK.
type Client struct {
	modelClient       modelClient
	videoClient       videoClient
	interactionClient interactionClient
	// interactionRetries is the retry configuration passed to Interactions calls.
	// Interactions does not read genai's HTTPRetryOptions, so it is built separately from Config.
	interactionRetries *retry.Config
	requestTimeout     time.Duration
	// backendName is the path name put in error messages ("Vertex AI" / "Gemini API").
	// Billing errors such as 402 have different causes depending on the path, so they are named apart.
	backendName string
}

// New creates a new client based on the provided configuration.
// Config decides the backend (Vertex AI by default).
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
		return nil, fmt.Errorf("gemini: failed to create client: %w", err)
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

// requestContext returns a context with Config.RequestTimeout applied.
// If unset (0), the caller's context is used as is.
func (c *Client) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return withRequestTimeout(ctx, c.requestTimeout)
}

// backend returns the path name put in error messages. It defaults to Vertex AI when unset, so the
// message does not break for a Client built without going through New (tests).
func (c *Client) backend() string {
	if c.backendName == "" {
		return "Vertex AI"
	}
	return c.backendName
}

// withRequestTimeout returns a context with a deadline only when timeout is positive.
func withRequestTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}
