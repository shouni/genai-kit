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

// These assertions guarantee at compile time that InteractionsGenerator satisfies Generator.
var _ Generator = (*InteractionsGenerator)(nil)

// InteractionsConfig is the configuration for Client.Interactions.
type InteractionsConfig struct {
	// AudioMIMEType is the output format requested from models that return audio ("audio/wav", etc.).
	// If empty, no format is specified and the model's default applies (MP3 for Lyria).
	//
	// If a call's GenerateOptions.ResponseMIMEType starts with "audio/", that one takes
	// precedence. The API rejects a format specified for a model that does not return audio, so
	// keep an InteractionsGenerator with this set dedicated to audio generation.
	AudioMIMEType string
}

// InteractionsGenerator is a Generator that generates through the Interactions API.
//
// generateContent has no way to specify the audio output format, and Lyria can only return MP3.
// Interactions can choose the format with response_format, so the intended use is to divert audio
// generation alone here (pass it to lyria.WithAudioGenerator). Text generation such as lyrics and
// composition can stay on the usual Client.Generate.
//
// Interactions has fewer generation parameters than generateContent, and when GenerateOptions that
// cannot be mapped (sampling, thinking, image settings, structured output, etc.) are specified, it
// returns ErrUnsupportedOption. Dropping them silently would bill a generation with settings the
// caller thought were applied having no effect.
type InteractionsGenerator struct {
	client         interactionClient
	retries        *retry.Config
	requestTimeout time.Duration
	audioMIMEType  string
}

// Interactions returns an InteractionsGenerator that inherits this client's authentication,
// retry and timeout settings.
func (c *Client) Interactions(cfg InteractionsConfig) *InteractionsGenerator {
	return &InteractionsGenerator{
		client:         c.interactionClient,
		retries:        c.interactionRetries,
		requestTimeout: c.requestTimeout,
		audioMIMEType:  cfg.AudioMIMEType,
	}
}

// Generate generates once through the Interactions API from a text prompt and attachments.
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
		return nil, fmt.Errorf("model %s Interactions call failed: %w", model, err)
	}
	if resp == nil || resp.Interaction == nil {
		return nil, &APIResponseError{Reason: ErrEmptyResponse, Message: "Interactions response was empty"}
	}
	return responseFromInteraction(resp.Interaction)
}

// buildModelInteraction maps Generate's arguments onto an Interactions request body.
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

// checkInteractionOptions refuses GenerateOptions that cannot be mapped onto Interactions.
func checkInteractionOptions(opts GenerateOptions) error {
	var unsupported []string
	// Temperature and TopP exist in Interactions' generation_config, but are deprecated in the
	// SDK, so they are not mapped.
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
	if opts.OutputURIPrefix != "" {
		unsupported = append(unsupported, "OutputURIPrefix")
	}
	if len(unsupported) > 0 {
		return fmt.Errorf("%w: %s", ErrUnsupportedOption, strings.Join(unsupported, ", "))
	}
	return nil
}

// interactionInput maps the prompt and attachments onto Interactions input.
//
// Attachments are handled the same way as attachmentParts (empty attachments are dropped, Data and
// URI are mutually exclusive, inline data requires a MIME type). The difference is that a URI also
// needs a MIME type: Interactions types content blocks by kind (image, audio), so a reference of
// unknown kind cannot be built. Text alone is sent as a string (the same shape as the official example).
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

// interactionContent maps one attachment onto a content block according to its MIME type.
func interactionContent(attachment Attachment, field string) (ia.Content, error) {
	var data, uri *string
	if attachment.URI != "" {
		uri = new(attachment.URI)
	} else {
		data = new(base64.StdEncoding.EncodeToString(attachment.Data))
	}

	switch {
	case attachment.MIMEType == "":
		return ia.Content{}, fmt.Errorf("%w: %s has no MIME type", ErrInvalidAttachment, field)
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
		return ia.Content{}, fmt.Errorf("%w: %s MIME type %q cannot be sent through Interactions (images and audio only)", ErrInvalidAttachment, field, attachment.MIMEType)
	}
}

// interactionGenerationConfig maps the fields corresponding to Interactions' generation_config.
// If nothing is specified it returns nil, leaving it to the model's defaults.
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

// interactionSafetySettings maps genai's safety settings onto Interactions' notation.
//
// The same setting is spelled differently: genai uses "HARM_CATEGORY_HATE_SPEECH" / "BLOCK_ONLY_HIGH",
// Interactions uses "hate_speech" / "block_only_high". It converts here so that values built with
// NewSafetySettings can be passed as is.
//
// BLOCK_NONE alone is mapped to "off". The SDK's enum has block_none, but the API rejects it for
// every category with "The value 'block_none' is not supported" (confirmed against the live API on
// 2026-10-18). The setting that matches the intent of not blocking is off.
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

// responseFromInteraction converts an Interactions result into the package's public Response type.
//
// The output is laid out as content blocks in the steps' model_output. Lyria returns the score text
// and the audio in separate steps, so every step is walked in order, the text is concatenated, and
// the data is stacked into Attachments in the order returned.
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
				Message: "Interactions output contained an error: " + statusMessage(output.Error),
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
		return nil, &APIResponseError{Reason: ErrEmptyResponse, Message: "Interactions response had no output"}
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

// decodeInteractionMedia turns a content block's data (base64) back into an Attachment.
// If it was returned as a URI, it is passed on as the URI without downloading.
func decodeInteractionMedia(data, uri, mimeType *string) (Attachment, error) {
	attachment := Attachment{}
	if mimeType != nil {
		attachment.MIMEType = *mimeType
	}
	switch {
	case data != nil && *data != "":
		decoded, err := base64.StdEncoding.DecodeString(*data)
		if err != nil {
			return Attachment{}, fmt.Errorf("%w: could not decode Interactions output data: %w", ErrEmptyResponse, err)
		}
		attachment.Data = decoded
	case uri != nil:
		attachment.URI = *uri
	}
	return attachment, nil
}

// interactionStatusError classifies an interaction returned in a state other than completed as an error.
//
// incomplete, budget_exceeded and requires_continuation are partial results cut off at a limit, so
// they are ErrTruncated; anything else (failed, cancelled, in_progress returned unfinished, etc.)
// has no usable output, so it is ErrEmptyResponse.
//
// requires_continuation marks a stop at the per-request output limit. Since genai v1.77.0 the SDK
// fetches the rest and joins it by default (automatic continuation), so it only reaches here when
// it remains after continuing.
// This matches treating generateContent's CONTINUATION as ErrTruncated.
func interactionStatusError(it *ia.Interaction) *APIResponseError {
	reason := ErrEmptyResponse
	switch it.Status {
	case ia.InteractionStatusIncomplete, ia.InteractionStatusBudgetExceeded,
		ia.InteractionStatusRequiresContinuation:
		reason = ErrTruncated
	}

	message := fmt.Sprintf("Interactions generation did not complete (status: %s)", it.Status)
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

// statusMessage returns the description of a Status. If there is no message, the code stands in.
func statusMessage(s *ia.Status) string {
	if s.Message != nil && *s.Message != "" {
		return *s.Message
	}
	if s.Code != nil {
		return fmt.Sprintf("code %d", *s.Code)
	}
	return "no details"
}

// tokenUsageFromInteraction maps Interactions' usage onto TokenUsage.
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
