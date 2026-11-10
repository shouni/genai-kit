package gemini

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"google.golang.org/genai"
)

// Generator is the interface that generates content from text and binary attachments.
//
// It contains no genai types, so callers depending only on this interface do not need to
// import the genai SDK. A mock needs only one method.
type Generator interface {
	Generate(ctx context.Context, model string, prompt string, attachments []Attachment, opts GenerateOptions) (*Response, error)
}

// GenerateText is the shortest path for generating content from a plain text prompt with no options.
func (c *Client) GenerateText(ctx context.Context, model string, prompt string) (*Response, error) {
	if prompt == "" {
		return nil, ErrEmptyPrompt
	}
	parts := []*genai.Part{{Text: prompt}}
	return c.generateParts(ctx, model, parts, GenerateOptions{})
}

// Generate generates content from a text prompt and binary attachments.
//
// It is the generation entry point without genai types, narrowed to the shape that makes up
// most multimodal calls: one text and n attachments. If there are no attachments, pass nil
// for attachments (it then serves as the "prompt + GenerateOptions" entry point).
//
// Even with an empty prompt, the request is sent if there are attachments (for passing only
// audio or images to have them analysed).
// It returns an error when both are empty, and when an attachment carrying data has no MIME type.
func (c *Client) Generate(ctx context.Context, model string, prompt string, attachments []Attachment, opts GenerateOptions) (*Response, error) {
	parts, err := attachmentParts(prompt, attachments)
	if err != nil {
		return nil, err
	}
	return c.generateParts(ctx, model, parts, opts)
}

// generateParts is the shared path for generating content from multimodal parts.
//
// The public entry points are GenerateText / Generate, which carry no genai types; this
// function, which takes genai.Part directly, is not exported (to keep SDK types off the public surface).
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

// buildThinkingConfig builds the thinking configuration.
// It returns nil when nothing is specified, because always sending it would override the model's default thinking behaviour.
//
// ThinkingLevel (a level) and ThinkingBudget (a token count) are mutually exclusive ways to specify it.
// When both are set, ThinkingLevel, which is model-independent and more portable, takes precedence.
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

// applyResponseFormat applies the response MIME type, modalities and structured-output schema.
func applyResponseFormat(genConfig *genai.GenerateContentConfig, opts GenerateOptions) {
	if opts.ResponseMIMEType != "" {
		genConfig.ResponseMIMEType = opts.ResponseMIMEType

		if strings.HasPrefix(opts.ResponseMIMEType, "audio/") {
			genConfig.ResponseModalities = []string{"AUDIO"}
		} else if strings.HasPrefix(opts.ResponseMIMEType, "image/") {
			genConfig.ResponseModalities = []string{"IMAGE"}
		}
	}
	// ResponseJSONSchema and ResponseSchema are mutually exclusive. Sending both leaves it undefined
	// which one takes effect, so only the newer ResponseJSONSchema is sent.
	switch {
	case opts.ResponseJSONSchema != nil:
		genConfig.ResponseJsonSchema = opts.ResponseJSONSchema
	case opts.ResponseSchema != nil:
		genConfig.ResponseSchema = opts.ResponseSchema
	}
}

// applyImageConfig applies the settings specific to image generation (Imagen/Nano Banana).
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

// buildGenerateConfig converts GenerateOptions into genai's generation config.
// It is not a Client method because it does not depend on Client state, so it can be
// tested without a client.
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

	if opts.OutputURIPrefix != "" {
		// A bad destination is known before sending rather than after generating, so stop here.
		if !strings.HasPrefix(opts.OutputURIPrefix, "gs://") {
			return nil, fmt.Errorf("%w (got: %q)", ErrInvalidOutputURIPrefix, opts.OutputURIPrefix)
		}
		genConfig.GenerationOutputConfig = &genai.GenerationOutputConfig{OutputURIPrefix: opts.OutputURIPrefix}
	}

	return genConfig, nil
}

// generate encapsulates the shared API call.
// Retries are the genai SDK's built-in ones (configured by Config.retryOptions).
// When Config.RequestTimeout is set, it applies to the whole call including retries
// (the SDK's retry loop checks ctx.Err(), so it never runs past the deadline).
func (c *Client) generate(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*Response, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()

	resp, err := c.modelClient.GenerateContent(ctx, model, contents, config)
	if err != nil {
		return nil, fmt.Errorf("model %s %s call failed: %w", model, c.backend(), err)
	}
	return responseFromGenAI(resp, config.GenerationOutputConfig != nil)
}

// seedToPtrInt32 converts *int64 to the SDK's *int32.
func seedToPtrInt32(s *int64) (*int32, error) {
	if s == nil {
		return nil, nil
	}

	if *s > math.MaxInt32 || *s < math.MinInt32 {
		return nil, fmt.Errorf("%w (got: %d)", ErrInvalidSeed, *s)
	}

	return new(int32(*s)), nil
}
