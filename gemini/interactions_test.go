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

// newTestInteractions は、フェイクの Interactions クライアントを持つ InteractionsGenerator を返します。
func newTestInteractions(fake *fakeInteractionClient, cfg InteractionsConfig) *InteractionsGenerator {
	client := &Client{interactionClient: fake, interactionRetries: Config{}.interactionRetryConfig()}
	return client.Interactions(cfg)
}

// interactionFromJSON は API の返す JSON から Interaction を組み立てます。
// 実際のレスポンスと同じ形（steps の model_output に内容ブロック）で検証するためです。
func interactionFromJSON(t *testing.T, raw string) *operations.CreateInteractionResponse {
	t.Helper()
	var it ia.Interaction
	if err := json.Unmarshal([]byte(raw), &it); err != nil {
		t.Fatalf("Interaction の JSON を読めません: %v", err)
	}
	return &operations.CreateInteractionResponse{Interaction: &it}
}

// sentBody は、フェイクに送られたリクエスト本文を API へ送る JSON の形で返します。
func sentBody(t *testing.T, fake *fakeInteractionClient) map[string]any {
	t.Helper()
	body := fake.gotReq.Body.CreateModelInteraction
	if body == nil {
		t.Fatal("CreateModelInteraction が送られていません")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("本文を JSON にできません: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("本文の JSON を読めません: %v", err)
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
		t.Fatalf("予期しないエラー: %v", err)
	}

	body := sentBody(t, fake)
	if body["model"] != "lyria-3.5" {
		t.Errorf("model = %v", body["model"])
	}
	// テキストだけなら文字列で送る（公式の例と同じ形）。
	if body["input"] != "piano" {
		t.Errorf("input = %v, want 文字列 \"piano\"", body["input"])
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
	// genai の "HARM_CATEGORY_HARASSMENT" は Interactions の小文字表記へ写す。BLOCK_NONE は
	// API が受け付けないため off へ写す。
	safety := jsonOf(t, body["safety_settings"])
	if strings.Contains(safety, "block_none") {
		t.Errorf("API が拒否する block_none を送っています: %s", safety)
	}
	for _, want := range []string{`"type":"harassment"`, `"type":"hate_speech"`, `"threshold":"off"`} {
		if !strings.Contains(safety, want) {
			t.Errorf("safety_settings に %s がありません: %s", want, safety)
		}
	}
	if len(fake.gotOpts) != 1 {
		t.Errorf("リトライ設定が渡っていません（opts = %d 件）", len(fake.gotOpts))
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
		want       string // 空なら response_format を送らない
	}{
		{name: "既定の形式を使う", defaultFmt: "audio/wav", want: "audio/wav"},
		{name: "呼び出しごとの音声形式が優先", defaultFmt: "audio/wav", perCall: "audio/mp3", want: "audio/mp3"},
		{name: "どちらも無ければ送らない"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeInteractionClient{resp: interactionFromJSON(t, lyriaWAVResponse)}
			g := newTestInteractions(fake, InteractionsConfig{AudioMIMEType: tt.defaultFmt})

			if _, err := g.Generate(context.Background(), "lyria-3.5", "piano", nil, GenerateOptions{ResponseMIMEType: tt.perCall}); err != nil {
				t.Fatalf("予期しないエラー: %v", err)
			}
			format, ok := sentBody(t, fake)["response_format"]
			if tt.want == "" {
				if ok {
					t.Errorf("response_format を送らないはずが %v", format)
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
	t.Run("画像はテキストの後に内容ブロックで送る", func(t *testing.T) {
		fake := &fakeInteractionClient{resp: interactionFromJSON(t, lyriaWAVResponse)}
		g := newTestInteractions(fake, InteractionsConfig{})

		_, err := g.Generate(context.Background(), "lyria-3.5", "piano", []Attachment{
			{},
			{MIMEType: "image/png", Data: []byte("png")},
			{MIMEType: "image/jpeg", URI: "gs://bucket/a.jpg"},
		}, GenerateOptions{})
		if err != nil {
			t.Fatalf("予期しないエラー: %v", err)
		}

		input, ok := sentBody(t, fake)["input"].([]any)
		if !ok || len(input) != 3 {
			t.Fatalf("input = %v, want 3 ブロック（空の添付は落とす）", sentBody(t, fake)["input"])
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
		{name: "URI でも MIME type が要る", attachments: []Attachment{{URI: "gs://bucket/a.png"}}, wantErr: ErrInvalidAttachment},
		{name: "画像と音声以外は送れない", attachments: []Attachment{{MIMEType: "application/pdf", Data: []byte("pdf")}}, wantErr: ErrInvalidAttachment},
		{name: "Data と URI は排他", attachments: []Attachment{{MIMEType: "image/png", Data: []byte("x"), URI: "gs://b/x"}}, wantErr: ErrInvalidAttachment},
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
				t.Errorf("不正な入力で API を呼んでいます（%d 回）", fake.calls)
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
		{name: "モデル名が空", prompt: "p", wantErr: ErrEmptyModelName},
		{name: "入力が空", model: "m", wantErr: ErrEmptyParts},
		{name: "シードが int32 を超える", model: "m", prompt: "p", opts: GenerateOptions{Seed: new(int64(1 << 40))}, wantErr: ErrInvalidSeed},
		{name: "温度は写せない", model: "m", prompt: "p", opts: GenerateOptions{Temperature: new(float32(0))}, wantErr: ErrUnsupportedOption},
		{name: "思考は写せない", model: "m", prompt: "p", opts: GenerateOptions{ThinkingLevel: ThinkingLow}, wantErr: ErrUnsupportedOption},
		{name: "画像設定は写せない", model: "m", prompt: "p", opts: GenerateOptions{AspectRatio: "1:1"}, wantErr: ErrUnsupportedOption},
		{name: "構造化出力は写せない", model: "m", prompt: "p", opts: GenerateOptions{ResponseJSONSchema: map[string]any{}}, wantErr: ErrUnsupportedOption},
		{name: "音声以外の MIME type は写せない", model: "m", prompt: "p", opts: GenerateOptions{ResponseMIMEType: "application/json"}, wantErr: ErrUnsupportedOption},
		{name: "GCS への書き出しは写せない", model: "m", prompt: "p", opts: GenerateOptions{OutputURIPrefix: "gs://bucket/out/"}, wantErr: ErrUnsupportedOption},
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
				t.Errorf("送る前に止めるはずが API を呼んでいます（%d 回）", fake.calls)
			}
		})
	}
}

func TestInteractionsGenerateReadsSteps(t *testing.T) {
	fake := &fakeInteractionClient{resp: interactionFromJSON(t, lyriaWAVResponse)}
	g := newTestInteractions(fake, InteractionsConfig{AudioMIMEType: "audio/wav"})

	resp, err := g.Generate(context.Background(), "lyria-3.5", "piano", nil, GenerateOptions{})
	if err != nil {
		t.Fatalf("予期しないエラー: %v", err)
	}

	if resp.Text != "[[A0]]\n[[B1]]" {
		t.Errorf("Text = %q", resp.Text)
	}
	if len(resp.Attachments) != 1 || resp.Attachments[0].MIMEType != "audio/wav" || string(resp.Attachments[0].Data) != "RIFF" {
		t.Errorf("Attachments = %+v, want audio/wav の RIFF 1 件", resp.Attachments)
	}
	if len(resp.Audios) != 1 || len(resp.Images) != 0 {
		t.Errorf("Audios = %d 件, Images = %d 件", len(resp.Audios), len(resp.Images))
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
			name:    "上限で打ち切られた途中結果",
			raw:     `{"status": "incomplete"}`,
			wantErr: ErrTruncated,
		},
		{
			// 自動継続（genai v1.77.0 から既定）を通っても続きが残った場合。generateContent の
			// CONTINUATION と同じく、拒否ではなく打ち切りとして扱う。
			name:    "出力上限で止まったまま残った",
			raw:     `{"status": "requires_continuation", "continuation_token": "t"}`,
			wantErr: ErrTruncated,
		},
		{
			name:        "失敗は API のメッセージを残す",
			raw:         `{"status": "failed", "errors": [{"code": "x", "message": "vocal likeness"}]}`,
			wantErr:     ErrEmptyResponse,
			wantMessage: "vocal likeness",
		},
		{
			name:    "完了しても出力が無い",
			raw:     `{"status": "completed", "steps": []}`,
			wantErr: ErrEmptyResponse,
		},
		{
			name:        "出力ステップのエラー",
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
				t.Errorf("APIResponseError として取り出せません: %v", err)
			}
			if tt.wantMessage != "" && !strings.Contains(err.Error(), tt.wantMessage) {
				t.Errorf("err = %v, want %q を含む", err, tt.wantMessage)
			}
		})
	}
}

// 自動継続が途中で止まると、SDK は ContinuationInterruptedError を返します。呼び出し側は
// タイムアウトを errors.Is(err, context.DeadlineExceeded) で見分けるので、原因が鎖に残ることを
// 確かめます。
func TestInteractionsGenerateKeepsTheCauseOfAnInterruptedContinuation(t *testing.T) {
	fake := &fakeInteractionClient{err: &apierrors.ContinuationInterruptedError{
		Message: "continuation interrupted",
		Cause:   context.DeadlineExceeded,
	}}
	g := newTestInteractions(fake, InteractionsConfig{})

	_, err := g.Generate(context.Background(), "lyria-3.5", "piano", nil, GenerateOptions{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded を含む", err)
	}
	if _, ok := errors.AsType[*apierrors.ContinuationInterruptedError](err); !ok {
		t.Errorf("ContinuationInterruptedError として取り出せません: %v", err)
	}
}

func TestInteractionsGenerateWrapsTransportError(t *testing.T) {
	sentinel := errors.New("402 prepayment credits are depleted")
	fake := &fakeInteractionClient{err: sentinel}
	g := newTestInteractions(fake, InteractionsConfig{})

	_, err := g.Generate(context.Background(), "lyria-3.5", "piano", nil, GenerateOptions{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("元のエラーを辿れません: %v", err)
	}
	// どの経路の失敗かを文面で分かるようにする（Vertex AI と取り違えない）。
	if !strings.Contains(err.Error(), "Interactions") || !strings.Contains(err.Error(), "lyria-3.5") {
		t.Errorf("err = %v, want 経路名とモデル名を含む", err)
	}
}

func TestConfigInteractionRetryConfig(t *testing.T) {
	t.Run("DisableRetry は既定ではなく none を返す", func(t *testing.T) {
		got := Config{DisableRetry: true}.interactionRetryConfig()
		if got == nil || got.Strategy != "none" {
			t.Errorf("got = %+v, want Strategy none", got)
		}
	})

	t.Run("既定値は generateContent 側と同じ方針", func(t *testing.T) {
		got := Config{}.interactionRetryConfig()
		if got.Strategy != "attempt-count-backoff" || got.MaxRetries == nil || *got.MaxRetries != int(DefaultMaxRetries) {
			t.Errorf("got = %+v", got)
		}
		if got.Backoff.InitialInterval != int(DefaultInitialDelay.Milliseconds()) || got.Backoff.MaxInterval != int(DefaultMaxDelay.Milliseconds()) {
			t.Errorf("Backoff = %+v", got.Backoff)
		}
	})

	t.Run("指定値を写す", func(t *testing.T) {
		got := Config{MaxRetries: 2}.interactionRetryConfig()
		if *got.MaxRetries != 2 {
			t.Errorf("MaxRetries = %d, want 2", *got.MaxRetries)
		}
	})
}
