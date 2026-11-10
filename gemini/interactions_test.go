package gemini

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"google.golang.org/genai/interactions/models/apierrors"
	ia "google.golang.org/genai/interactions/models/interactions"
	"google.golang.org/genai/interactions/models/operations"
)

// newTestInteractions returns an InteractionsGenerator holding a fake Interactions client.
func newTestInteractions(fake *fakeInteractionClient, cfg InteractionsConfig) *InteractionsGenerator {
	client := &Client{interactionClient: fake, interactionRetries: Config{}.interactionRetryConfig()}
	return client.Interactions(cfg)
}

// interactionFromJSON builds an Interaction from the JSON the API returns.
// This tests in the same shape as a real response (content blocks in the steps' model_output).
func interactionFromJSON(t *testing.T, raw string) *operations.CreateInteractionResponse {
	t.Helper()
	var it ia.Interaction
	if err := json.Unmarshal([]byte(raw), &it); err != nil {
		t.Fatalf("cannot read the Interaction JSON: %v", err)
	}
	return &operations.CreateInteractionResponse{Interaction: &it}
}

// sentBody returns the request body sent to the fake in the shape of the JSON sent to the API.
func sentBody(t *testing.T, fake *fakeInteractionClient) map[string]any {
	t.Helper()
	body := fake.gotReq.Body.CreateModelInteraction
	if body == nil {
		t.Fatal("CreateModelInteraction was not sent")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("cannot turn the body into JSON: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("cannot read the body JSON: %v", err)
	}
	return decoded
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

const lyriaWAVResponse = `{
  "status": "completed",
  "usage": {"total_input_tokens": 5, "total_output_tokens": 2177, "total_tokens": 2182},
  "steps": [
    {"type": "model_output", "content": [{"type": "text", "text": "[[A0]]\n[[B1]]"}]},
    {"type": "model_output", "content": [{"type": "audio", "mime_type": "audio/wav", "data": "UklGRg=="}]}
  ]
}`

func TestInteractionsGenerateBuildsAudioRequest(t *testing.T) {
	fake := &fakeInteractionClient{resp: interactionFromJSON(t, lyriaWAVResponse)}
	g := newTestInteractions(fake, InteractionsConfig{AudioMIMEType: "audio/wav"})

	_, err := g.Generate(context.Background(), "lyria-3.5", "piano", nil, GenerateOptions{
		Seed:           new(int64(42)),
		SafetySettings: NewSafetySettings(SafetyBlockNone),
		SystemPrompt:   "sys",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body := sentBody(t, fake)
	if body["model"] != "lyria-3.5" {
		t.Errorf("model = %v", body["model"])
	}
	// Text alone is sent as a string (the same shape as the official example).
	if body["input"] != "piano" {
		t.Errorf("input = %v, want the string \"piano\"", body["input"])
	}
	if got := jsonOf(t, body["response_format"]); got != `{"mime_type":"audio/wav","type":"audio"}` {
		t.Errorf("response_format = %s", got)
	}
	if got := jsonOf(t, body["generation_config"]); got != `{"seed":42}` {
		t.Errorf("generation_config = %s", got)
	}
	if body["system_instruction"] != "sys" {
		t.Errorf("system_instruction = %v", body["system_instruction"])
	}
	// genai's "HARM_CATEGORY_HARASSMENT" maps to Interactions' lowercase notation. BLOCK_NONE maps
	// to off because the API does not accept it.
	safety := jsonOf(t, body["safety_settings"])
	if strings.Contains(safety, "block_none") {
		t.Errorf("sending block_none, which the API rejects: %s", safety)
	}
	for _, want := range []string{`"type":"harassment"`, `"type":"hate_speech"`, `"threshold":"off"`} {
		if !strings.Contains(safety, want) {
			t.Errorf("%s is missing from safety_settings: %s", want, safety)
		}
	}
	if len(fake.gotOpts) != 1 {
		t.Errorf("retry settings were not passed (opts = %d entries)", len(fake.gotOpts))
	}
}

func TestInteractionSafetySettingsKeepsOtherThresholds(t *testing.T) {
	got := jsonOf(t, interactionSafetySettings(NewSafetySettings(SafetyBlockOnlyHigh)))
	if !strings.Contains(got, `"threshold":"block_only_high"`) || !strings.Contains(got, `"type":"dangerous_content"`) {
		t.Errorf("safety_settings = %s", got)
	}
}

func TestInteractionsGenerateFormatPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		defaultFmt string
		perCall    string
		want       string // if empty, response_format is not sent
	}{
		{name: "uses the default format", defaultFmt: "audio/wav", want: "audio/wav"},
		{name: "the per-call audio format takes precedence", defaultFmt: "audio/wav", perCall: "audio/mp3", want: "audio/mp3"},
		{name: "not sent when neither is set"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeInteractionClient{resp: interactionFromJSON(t, lyriaWAVResponse)}
			g := newTestInteractions(fake, InteractionsConfig{AudioMIMEType: tt.defaultFmt})

			if _, err := g.Generate(context.Background(), "lyria-3.5", "piano", nil, GenerateOptions{ResponseMIMEType: tt.perCall}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			format, ok := sentBody(t, fake)["response_format"]
			if tt.want == "" {
				if ok {
					t.Errorf("response_format should not be sent, got %v", format)
				}
				return
			}
			if got := jsonOf(t, format); !strings.Contains(got, `"mime_type":"`+tt.want+`"`) {
				t.Errorf("response_format = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestInteractionsGenerateAttachments(t *testing.T) {
	t.Run("images are sent as content blocks after the text", func(t *testing.T) {
		fake := &fakeInteractionClient{resp: interactionFromJSON(t, lyriaWAVResponse)}
		g := newTestInteractions(fake, InteractionsConfig{})

		_, err := g.Generate(context.Background(), "lyria-3.5", "piano", []Attachment{
			{},
			{MIMEType: "image/png", Data: []byte("png")},
			{MIMEType: "image/jpeg", URI: "gs://bucket/a.jpg"},
		}, GenerateOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		input, ok := sentBody(t, fake)["input"].([]any)
		if !ok || len(input) != 3 {
			t.Fatalf("input = %v, want 3 blocks (empty attachments are dropped)", sentBody(t, fake)["input"])
		}
		want := []string{
			`{"text":"piano","type":"text"}`,
			`{"data":"` + base64.StdEncoding.EncodeToString([]byte("png")) + `","mime_type":"image/png","type":"image"}`,
			`{"mime_type":"image/jpeg","type":"image","uri":"gs://bucket/a.jpg"}`,
		}
		for i, block := range input {
			if got := jsonOf(t, block); got != want[i] {
				t.Errorf("input[%d] = %s, want %s", i, got, want[i])
			}
		}
	})

	tests := []struct {
		name        string
		attachments []Attachment
		wantErr     error
	}{
		{name: "a URI also needs a MIME type", attachments: []Attachment{{URI: "gs://bucket/a.png"}}, wantErr: ErrInvalidAttachment},
		{name: "nothing but images and audio can be sent", attachments: []Attachment{{MIMEType: "application/pdf", Data: []byte("pdf")}}, wantErr: ErrInvalidAttachment},
		{name: "Data and URI are mutually exclusive", attachments: []Attachment{{MIMEType: "image/png", Data: []byte("x"), URI: "gs://b/x"}}, wantErr: ErrInvalidAttachment},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeInteractionClient{}
			g := newTestInteractions(fake, InteractionsConfig{})

			_, err := g.Generate(context.Background(), "lyria-3.5", "piano", tt.attachments, GenerateOptions{})
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if fake.calls != 0 {
				t.Errorf("the API was called with invalid input (%d times)", fake.calls)
			}
		})
	}
}

func TestInteractionsGenerateRejectsBeforeSending(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		prompt  string
		opts    GenerateOptions
		wantErr error
	}{
		{name: "empty model name", prompt: "p", wantErr: ErrEmptyModelName},
		{name: "empty input", model: "m", wantErr: ErrEmptyParts},
		{name: "seed exceeds int32", model: "m", prompt: "p", opts: GenerateOptions{Seed: new(int64(1 << 40))}, wantErr: ErrInvalidSeed},
		{name: "temperature cannot be mapped", model: "m", prompt: "p", opts: GenerateOptions{Temperature: new(float32(0))}, wantErr: ErrUnsupportedOption},
		{name: "thinking cannot be mapped", model: "m", prompt: "p", opts: GenerateOptions{ThinkingLevel: ThinkingLow}, wantErr: ErrUnsupportedOption},
		{name: "image settings cannot be mapped", model: "m", prompt: "p", opts: GenerateOptions{AspectRatio: "1:1"}, wantErr: ErrUnsupportedOption},
		{name: "structured output cannot be mapped", model: "m", prompt: "p", opts: GenerateOptions{ResponseJSONSchema: map[string]any{}}, wantErr: ErrUnsupportedOption},
		{name: "a non-audio MIME type cannot be mapped", model: "m", prompt: "p", opts: GenerateOptions{ResponseMIMEType: "application/json"}, wantErr: ErrUnsupportedOption},
		{name: "writing to GCS cannot be mapped", model: "m", prompt: "p", opts: GenerateOptions{OutputURIPrefix: "gs://bucket/out/"}, wantErr: ErrUnsupportedOption},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeInteractionClient{}
			g := newTestInteractions(fake, InteractionsConfig{})

			_, err := g.Generate(context.Background(), tt.model, tt.prompt, nil, tt.opts)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if fake.calls != 0 {
				t.Errorf("should stop before sending, but the API was called (%d times)", fake.calls)
			}
		})
	}
}

func TestInteractionsGenerateReadsSteps(t *testing.T) {
	fake := &fakeInteractionClient{resp: interactionFromJSON(t, lyriaWAVResponse)}
	g := newTestInteractions(fake, InteractionsConfig{AudioMIMEType: "audio/wav"})

	resp, err := g.Generate(context.Background(), "lyria-3.5", "piano", nil, GenerateOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Text != "[[A0]]\n[[B1]]" {
		t.Errorf("Text = %q", resp.Text)
	}
	if len(resp.Attachments) != 1 || resp.Attachments[0].MIMEType != "audio/wav" || string(resp.Attachments[0].Data) != "RIFF" {
		t.Errorf("Attachments = %+v, want one audio/wav RIFF entry", resp.Attachments)
	}
	if len(resp.Audios) != 1 || len(resp.Images) != 0 {
		t.Errorf("Audios = %d entries, Images = %d entries", len(resp.Audios), len(resp.Images))
	}
	if resp.Usage == nil || resp.Usage.PromptTokenCount != 5 || resp.Usage.CandidatesTokenCount != 2177 || resp.Usage.TotalTokenCount != 2182 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
}

func TestInteractionsGenerateClassifiesFailures(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantErr     error
		wantMessage string
	}{
		{
			name:    "partial result cut off at a limit",
			raw:     `{"status": "incomplete"}`,
			wantErr: ErrTruncated,
		},
		{
			// When the rest remains even after automatic continuation (the default since genai v1.77.0). Like
			// generateContent's CONTINUATION, it is treated as a truncation, not a refusal.
			name:    "left stopped at the output limit",
			raw:     `{"status": "requires_continuation", "continuation_token": "t"}`,
			wantErr: ErrTruncated,
		},
		{
			name:        "a failure keeps the API's message",
			raw:         `{"status": "failed", "errors": [{"code": "x", "message": "vocal likeness"}]}`,
			wantErr:     ErrEmptyResponse,
			wantMessage: "vocal likeness",
		},
		{
			name:    "completed but no output",
			raw:     `{"status": "completed", "steps": []}`,
			wantErr: ErrEmptyResponse,
		},
		{
			name:        "error in an output step",
			raw:         `{"status": "completed", "steps": [{"type": "model_output", "error": {"code": 3, "message": "bad"}}]}`,
			wantErr:     ErrEmptyResponse,
			wantMessage: "bad",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeInteractionClient{resp: interactionFromJSON(t, tt.raw)}
			g := newTestInteractions(fake, InteractionsConfig{})

			_, err := g.Generate(context.Background(), "lyria-3.5", "piano", nil, GenerateOptions{})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if _, ok := errors.AsType[*APIResponseError](err); !ok {
				t.Errorf("cannot be extracted as APIResponseError: %v", err)
			}
			if tt.wantMessage != "" && !strings.Contains(err.Error(), tt.wantMessage) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantMessage)
			}
		})
	}
}

// When automatic continuation stops midway, the SDK returns ContinuationInterruptedError. Callers
// tell a timeout apart with errors.Is(err, context.DeadlineExceeded), so this checks that the cause
// stays in the chain.
func TestInteractionsGenerateKeepsTheCauseOfAnInterruptedContinuation(t *testing.T) {
	fake := &fakeInteractionClient{err: &apierrors.ContinuationInterruptedError{
		Message: "continuation interrupted",
		Cause:   context.DeadlineExceeded,
	}}
	g := newTestInteractions(fake, InteractionsConfig{})

	_, err := g.Generate(context.Background(), "lyria-3.5", "piano", nil, GenerateOptions{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want it to contain context.DeadlineExceeded", err)
	}
	if _, ok := errors.AsType[*apierrors.ContinuationInterruptedError](err); !ok {
		t.Errorf("cannot be extracted as ContinuationInterruptedError: %v", err)
	}
}

func TestInteractionsGenerateWrapsTransportError(t *testing.T) {
	sentinel := errors.New("402 prepayment credits are depleted")
	fake := &fakeInteractionClient{err: sentinel}
	g := newTestInteractions(fake, InteractionsConfig{})

	_, err := g.Generate(context.Background(), "lyria-3.5", "piano", nil, GenerateOptions{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("cannot reach the original error: %v", err)
	}
	// Make it clear from the wording which path failed (not to be confused with Vertex AI).
	if !strings.Contains(err.Error(), "Interactions") || !strings.Contains(err.Error(), "lyria-3.5") {
		t.Errorf("err = %v, want it to contain the path name and model name", err)
	}
}

func TestConfigInteractionRetryConfig(t *testing.T) {
	t.Run("DisableRetry returns none rather than the default", func(t *testing.T) {
		got := Config{DisableRetry: true}.interactionRetryConfig()
		if got == nil || got.Strategy != "none" {
			t.Errorf("got = %+v, want Strategy none", got)
		}
	})

	t.Run("defaults follow the same policy as generateContent", func(t *testing.T) {
		got := Config{}.interactionRetryConfig()
		if got.Strategy != "attempt-count-backoff" || got.MaxRetries == nil || *got.MaxRetries != int(DefaultMaxRetries) {
			t.Errorf("got = %+v", got)
		}
		if got.Backoff.InitialInterval != int(DefaultInitialDelay.Milliseconds()) || got.Backoff.MaxInterval != int(DefaultMaxDelay.Milliseconds()) {
			t.Errorf("Backoff = %+v", got.Backoff)
		}
	})

	t.Run("copies the specified values", func(t *testing.T) {
		got := Config{MaxRetries: 2}.interactionRetryConfig()
		if *got.MaxRetries != 2 {
			t.Errorf("MaxRetries = %d, want 2", *got.MaxRetries)
		}
	})
}
