package imagegen

import (
	"testing"

	"github.com/shouni/genai-kit/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPrepared(seed *int64) preparedRequest {
	return preparedRequest{
		model:  "imagen-test",
		prompt: "a cat",
		opts:   gemini.GenerateOptions{Seed: seed},
	}
}

// TestToResponseTakesFirstInlineImage は、最初のインライン画像を MIME type ごと
// 取り出すことを検証します。
//
// gemini.Response.Attachments が MIME type 込みで返るため、保存時の Content-Type を
// 決めるのに生の SDK レスポンスを辿る必要はありません。
func TestToResponseTakesFirstInlineImage(t *testing.T) {
	seed := int64(42)
	usage := &gemini.TokenUsage{PromptTokenCount: 10, TotalTokenCount: 12}

	got, err := newPrepared(&seed).toResponse(&gemini.Response{
		Attachments: []gemini.Attachment{
			{MIMEType: "image/png"}, // データの無い添付は読み飛ばす
			{MIMEType: "image/webp", Data: []byte("webp-bytes")},
			{MIMEType: "image/png", Data: []byte("png-bytes")},
		},
		Usage: usage,
	})
	require.NoError(t, err)

	assert.Equal(t, []byte("webp-bytes"), got.Data)
	assert.Equal(t, "image/webp", got.MIMEType)
	assert.Equal(t, int64(42), got.UsedSeed)
	assert.Equal(t, "imagen-test", got.Model)
	assert.Equal(t, "a cat", got.Prompt)
	assert.Same(t, usage, got.Usage)
}

// TestToResponseTakesOutputURI は、OutputURIPrefix の指定で GCS に書き込まれた画像を
// URI として返すことを検証します。サーバーが MIME type を添えなかった場合は、
// 書き込み先の拡張子から補います。
func TestToResponseTakesOutputURI(t *testing.T) {
	tests := []struct {
		name     string
		in       gemini.Attachment
		wantMIME string
	}{
		{"サーバーの MIME type をそのまま使う", gemini.Attachment{URI: "gs://b/out/1", MIMEType: "image/webp"}, "image/webp"},
		{"無ければ拡張子から補う", gemini.Attachment{URI: "gs://b/out/1.png"}, "image/png"},
		{"拡張子でも分からなければ空のまま", gemini.Attachment{URI: "gs://b/out/1"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newPrepared(nil).toResponse(&gemini.Response{
				Attachments: []gemini.Attachment{tt.in},
			})
			require.NoError(t, err)

			assert.Empty(t, got.Data)
			assert.Equal(t, tt.in.URI, got.URI)
			assert.Equal(t, tt.wantMIME, got.MIMEType)
		})
	}
}

// TestToResponseReportsMissingImage は、画像が無い応答を区別できるエラーにすることを
// 検証します。安全フィルタによるブロックは gemini 側でエラーになるため、このパッケージが
// 区別するのは「画像データが無い」ことだけです。
func TestToResponseReportsMissingImage(t *testing.T) {
	tests := []struct {
		name string
		resp *gemini.Response
	}{
		{"レスポンスが nil", nil},
		{"添付が無い", &gemini.Response{Text: "説明だけ"}},
		{"添付はあるがデータが空", &gemini.Response{Attachments: []gemini.Attachment{{MIMEType: "image/png"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newPrepared(nil).toResponse(tt.resp)

			assert.ErrorIs(t, err, ErrNoImageData)
		})
	}
}

// TestToResponseRecordsZeroSeedWhenUnset は、シード未指定（WithoutAutoSeed）で
// UsedSeed が 0 になることを検証します。自動採番が既定なのは、この 0 が「API が
// 選んだシード」と区別できないからです。
func TestToResponseRecordsZeroSeedWhenUnset(t *testing.T) {
	got, err := newPrepared(nil).toResponse(&gemini.Response{
		Attachments: []gemini.Attachment{{MIMEType: "image/png", Data: []byte("x")}},
	})
	require.NoError(t, err)

	assert.Zero(t, got.UsedSeed)
}
