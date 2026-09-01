package imagegen

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/shouni/genai-kit/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gemini.Generator は 1 メソッドなので、テストダブルは素直な構造体で書けます。
// これがこのパッケージの唯一の注入点です。
var _ gemini.Generator = (*fakeGenerator)(nil)

// generateCall は fakeGenerator が受け取った 1 回分の呼び出しです。
type generateCall struct {
	Model       string
	Prompt      string
	Attachments []gemini.Attachment
	Opts        gemini.GenerateOptions
}

type fakeGenerator struct {
	mu    sync.Mutex
	calls []generateCall

	resp *gemini.Response
	err  error
}

func (f *fakeGenerator) Generate(_ context.Context, model, prompt string, attachments []gemini.Attachment, opts gemini.GenerateOptions) (*gemini.Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, generateCall{Model: model, Prompt: prompt, Attachments: attachments, Opts: opts})
	f.mu.Unlock()

	return f.resp, f.err
}

func (f *fakeGenerator) lastCall(t *testing.T) generateCall {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.calls, "AI が 1 度も呼ばれていません")
	return f.calls[len(f.calls)-1]
}

// imageResponder は、インライン画像を 1 枚返す fakeGenerator を作ります。
func imageResponder(mimeType string, data []byte) *fakeGenerator {
	return &fakeGenerator{resp: &gemini.Response{
		Attachments: []gemini.Attachment{{MIMEType: mimeType, Data: data}},
	}}
}

func TestNewRequiresGenerator(t *testing.T) {
	client, err := New(nil)

	assert.ErrorIs(t, err, ErrGeneratorRequired)
	assert.Nil(t, client)
}

func TestNewIgnoresNilOption(t *testing.T) {
	client, err := New(&fakeGenerator{}, nil, WithoutAutoSeed())

	require.NoError(t, err)
	assert.False(t, client.autoSeed)
}

// TestGenerateSendsPromptAndReferences は、参照画像が gs:// のまま添付として渡り、
// レスポンスに送信内容の記録が付くことを検証します。
func TestGenerateSendsPromptAndReferences(t *testing.T) {
	ai := imageResponder("image/png", []byte("png-bytes"))
	client, err := New(ai)
	require.NoError(t, err)

	got, err := client.Generate(context.Background(), Request{
		Model:  "imagen-test",
		Prompt: "a cat on a roof",
		Images: []string{"gs://bucket/char.png", "gs://bucket/style.jpg"},
	})
	require.NoError(t, err)

	call := ai.lastCall(t)
	assert.Equal(t, "imagen-test", call.Model)
	assert.Equal(t, "a cat on a roof", call.Prompt)
	require.Len(t, call.Attachments, 2)
	// gs:// はモデル側で解決されるため、バイト列は送らない。
	assert.Equal(t, gemini.Attachment{URI: "gs://bucket/char.png", MIMEType: "image/png"}, call.Attachments[0])
	assert.Equal(t, gemini.Attachment{URI: "gs://bucket/style.jpg", MIMEType: "image/jpeg"}, call.Attachments[1])

	assert.Equal(t, []byte("png-bytes"), got.Data)
	assert.Equal(t, "image/png", got.MIMEType)
	assert.Equal(t, "imagen-test", got.Model)
	assert.Equal(t, "a cat on a roof", got.Prompt)
}

// TestGenerateMintsSeedBeforeSending は、シードを送信前に採番することを検証します。
//
// API はレスポンスに採番したシードを返さないため、API 任せにすると UsedSeed が 0 の
// まま記録され、同条件での再生成ができなくなります（0 は有効なシードなので、
// 「未記録」と区別も付きません）。
func TestGenerateMintsSeedBeforeSending(t *testing.T) {
	ai := imageResponder("image/png", []byte("x"))
	client, err := New(ai)
	require.NoError(t, err)

	got, err := client.Generate(context.Background(), Request{Model: "imagen-test", Prompt: "p"})
	require.NoError(t, err)

	sent := ai.lastCall(t).Opts.Seed
	require.NotNil(t, sent, "シードが送信されていません")
	assert.Equal(t, *sent, got.UsedSeed, "送信したシードと記録が食い違っています")
	assert.GreaterOrEqual(t, got.UsedSeed, int64(0))
	// gemini が int32 の範囲外を弾くため、採番も範囲内に収める。
	assert.Less(t, got.UsedSeed, int64(math.MaxInt32))
}

// TestGenerateRespectsExplicitSeed は、呼び出し側が指定したシードを上書きしない
// ことを検証します。
func TestGenerateRespectsExplicitSeed(t *testing.T) {
	seed := int64(4242)
	ai := imageResponder("image/png", []byte("x"))
	client, err := New(ai)
	require.NoError(t, err)

	got, err := client.Generate(context.Background(), Request{
		Model:           "imagen-test",
		Prompt:          "p",
		GenerateOptions: gemini.GenerateOptions{Seed: &seed},
	})
	require.NoError(t, err)

	assert.Equal(t, seed, got.UsedSeed)
	require.NotNil(t, ai.lastCall(t).Opts.Seed)
	assert.Equal(t, seed, *ai.lastCall(t).Opts.Seed)
}

// TestWithoutAutoSeedLeavesSeedToAPI は、自動採番を切ったときにシードを送らず、
// UsedSeed が 0 になることを検証します。シード管理を完全に呼び出し側へ渡す設定です。
func TestWithoutAutoSeedLeavesSeedToAPI(t *testing.T) {
	ai := imageResponder("image/png", []byte("x"))
	client, err := New(ai, WithoutAutoSeed())
	require.NoError(t, err)

	got, err := client.Generate(context.Background(), Request{Model: "imagen-test", Prompt: "p"})
	require.NoError(t, err)

	assert.Nil(t, ai.lastCall(t).Opts.Seed)
	assert.Zero(t, got.UsedSeed)
}

// TestGeneratePropagatesGenerationError は、生成呼び出しの失敗をそのまま返すことを
// 検証します。安全フィルタによるブロックは gemini 側でエラーになるため、ここで
// 握り潰すと呼び出し側が理由を判定できません。
func TestGeneratePropagatesGenerationError(t *testing.T) {
	sentinel := errors.New("blocked")
	client, err := New(&fakeGenerator{err: sentinel})
	require.NoError(t, err)

	_, err = client.Generate(context.Background(), Request{Model: "imagen-test", Prompt: "p"})

	assert.ErrorIs(t, err, sentinel)
}

// TestGenerateValidatesBeforeSending は、入力の不備で AI を呼ばないことを検証します。
func TestGenerateValidatesBeforeSending(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		want error
	}{
		{"モデル名が空", Request{Prompt: "p"}, ErrModelRequired},
		{"プロンプトが空", Request{Model: "m"}, ErrEmptyPrompt},
		{"空白だけのプロンプト", Request{Model: "m", Prompt: "   "}, ErrEmptyPrompt},
		{"gs:// 以外の参照", Request{Model: "m", Prompt: "p", Images: []string{"https://example.com/a.png"}}, ErrUnsupportedReference},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ai := &fakeGenerator{}
			client, err := New(ai)
			require.NoError(t, err)

			_, err = client.Generate(context.Background(), tt.req)

			assert.ErrorIs(t, err, tt.want)
			assert.Empty(t, ai.calls, "送信前に弾かれるべきです")
		})
	}
}
