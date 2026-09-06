package imagegen

import (
	"math"
	"strings"
	"testing"

	"github.com/shouni/genai-kit/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildFinalPromptRendersNegativePrompt は、ネガティブプロンプトの描画を
// 検証します。
//
// これは互換性の契約です。ネガティブプロンプトは API のフィールドではなく、
// 下流のプロンプト実装がこの区切りの見た目そのものに依存しています。
func TestBuildFinalPromptRendersNegativePrompt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		prompt   string
		negative string
		want     string
	}{
		{"どちらも空", "", "", ""},
		{"本文のみ", "a cat", "", "a cat"},
		{"ネガティブのみ", "", "blurry", "\n\n[Negative Prompt]\nblurry"},
		{"両方", "a cat", "blurry", "a cat\n\n[Negative Prompt]\nblurry"},
		{"前後の空白は落とす", "  a cat  ", "  blurry  ", "a cat\n\n[Negative Prompt]\nblurry"},
		{"空白だけは空扱い", "   ", "   ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, buildFinalPrompt(tt.prompt, tt.negative))
		})
	}
}

// TestNegativePromptSeparatorIsStable は、区切りの見た目そのものを固定します。
// 変えると下流のプロンプト実装のマッチが外れます。
func TestNegativePromptSeparatorIsStable(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "\n\n[Negative Prompt]\n", negativePromptSeparator)
}

// TestApplyDefaultsFillsOnlyUnsetFields は、明示された安全設定と人物生成を
// 上書きしないことを検証します。無条件に上書きすると、呼び出し側が安全フィルタを
// 厳しくする手段が無くなります。
func TestApplyDefaultsFillsOnlyUnsetFields(t *testing.T) {
	t.Parallel()

	t.Run("未設定なら既定値を補うこと", func(t *testing.T) {
		got := applyDefaults(gemini.GenerateOptions{})

		require.NotEmpty(t, got.SafetySettings)
		for _, setting := range got.SafetySettings {
			// Vertex AI は OFF を受け付けないため BLOCK_NONE を使う。
			assert.Equal(t, gemini.SafetyBlockNone, setting.Threshold)
		}
		// キャラクター生成が主用途のため、未指定時は人物生成を許可する。
		assert.Equal(t, gemini.PersonGenerationAllowAll, got.PersonGeneration)
	})

	t.Run("明示された値は尊重すること", func(t *testing.T) {
		strict := gemini.NewSafetySettings(gemini.SafetyBlockLowAndAbove)

		got := applyDefaults(gemini.GenerateOptions{
			SafetySettings:   strict,
			PersonGeneration: gemini.PersonGenerationDontAllow,
		})

		assert.Equal(t, strict, got.SafetySettings)
		assert.Equal(t, gemini.PersonGenerationDontAllow, got.PersonGeneration)
	})
}

// TestPrepareCombinesPromptAndDefaults は、検証・プロンプト結合・既定値の補完が
// 1 回で済むことを検証します。
func TestPrepareCombinesPromptAndDefaults(t *testing.T) {
	t.Parallel()

	client := &Client{ai: &fakeGenerator{}, autoSeed: false}

	got, err := client.prepare(Request{
		Model:          "imagen-test",
		Prompt:         "a cat",
		NegativePrompt: "blurry",
	})
	require.NoError(t, err)

	assert.Equal(t, "imagen-test", got.model)
	assert.Equal(t, "a cat"+negativePromptSeparator+"blurry", got.prompt)
	assert.NotEmpty(t, got.opts.SafetySettings)
	assert.Nil(t, got.opts.Seed, "自動採番を切っているのでシードは付かない")
}

// TestPrepareAcceptsNegativePromptOnly は、ネガティブプロンプトだけでも送信可能な
// ことを検証します。結合後の文字列が空でなければ有効な指示だからです。
func TestPrepareAcceptsNegativePromptOnly(t *testing.T) {
	t.Parallel()

	client := &Client{ai: &fakeGenerator{}}

	got, err := client.prepare(Request{Model: "m", NegativePrompt: "blurry"})

	require.NoError(t, err)
	assert.Contains(t, got.prompt, "blurry")
}

// TestReferenceAttachmentsKeepsOrderAndDropsEmpty は、参照画像の並び順が保たれ、
// 空文字列の要素が黙って外れることを検証します。
//
// 空要素を落とすのは、「このキャラクターには参照画像が無い」を呼び出し側が
// 要素の欠落として表現できるようにするためです。
func TestReferenceAttachmentsKeepsOrderAndDropsEmpty(t *testing.T) {
	t.Parallel()

	got, err := referenceAttachments(Request{Images: []string{
		"gs://bucket/a.png",
		"",
		"gs://bucket/b.webp",
	}})
	require.NoError(t, err)

	require.Len(t, got, 2)
	assert.Equal(t, "gs://bucket/a.png", got[0].URI)
	assert.Equal(t, "gs://bucket/b.webp", got[1].URI)
}

// TestReferenceAttachmentsHintsMIMETypeFromExtension は、拡張子から MIME type を推測し、
// 判別できない場合は申告しないことを検証します。誤った申告は受け取った側の解釈を
// 壊すため、サーバー側の判定に委ねます。
func TestReferenceAttachmentsHintsMIMETypeFromExtension(t *testing.T) {
	t.Parallel()

	got, err := referenceAttachments(Request{Images: []string{
		"gs://bucket/a.png",
		"gs://bucket/b.JPEG",
		"gs://bucket/c",
	}})
	require.NoError(t, err)

	require.Len(t, got, 3)
	assert.Equal(t, "image/png", got[0].MIMEType)
	assert.Equal(t, "image/jpeg", got[1].MIMEType)
	assert.Empty(t, got[2].MIMEType, "判別できない拡張子は申告しない")
}

func TestReferenceAttachmentsRejectsNonGCSURIs(t *testing.T) {
	t.Parallel()

	for _, uri := range []string{"https://example.com/a.png", "file:///tmp/a.png", "a.png"} {
		_, err := referenceAttachments(Request{Images: []string{uri}})

		assert.ErrorIs(t, err, ErrUnsupportedReference, "uri = %q", uri)
	}
}

// TestReferenceAttachmentsAcceptsInlineBytes は、呼び出し側が取得済みのバイト列を
// gs:// と混ぜた順序のまま渡せることを検証します。
//
// 順序は生成結果を変えるため、gs:// とバイト列を別のリストに分けると混在した並びを
// 表現できません。References が 1 本なのはそのためです。
func TestReferenceAttachmentsAcceptsInlineBytes(t *testing.T) {
	t.Parallel()

	got, err := referenceAttachments(Request{References: []gemini.Attachment{
		{URI: "gs://bucket/a.png"},
		{Data: []byte("fetched"), MIMEType: "image/webp"},
		{},
		{URI: "gs://bucket/b.png"},
	}})
	require.NoError(t, err)

	require.Len(t, got, 3, "送るものが無い要素は黙って外れる")
	assert.Equal(t, "gs://bucket/a.png", got[0].URI)
	assert.Equal(t, []byte("fetched"), got[1].Data)
	assert.Equal(t, "image/webp", got[1].MIMEType)
	assert.Equal(t, "gs://bucket/b.png", got[2].URI)
}

// TestReferenceAttachmentsRequiresMIMETypeForInlineBytes は、バイト列に MIME type が
// 要ることを検証します。バイト列からは型が決まらず、誤った申告は受け取り側の解釈を
// 壊すため、推測せずに要求します。
func TestReferenceAttachmentsRequiresMIMETypeForInlineBytes(t *testing.T) {
	t.Parallel()

	_, err := referenceAttachments(Request{References: []gemini.Attachment{
		{Data: []byte("fetched")},
	}})

	assert.ErrorIs(t, err, ErrMissingReferenceMIMEType)
}

// TestReferenceAttachmentsRejectsBothInputs は、Images と References の併用を
// 弾くことを検証します。2 本のリストの間の順序を決める根拠が無く、黙って連結すると
// 呼び出し側が意図しない並びで送られます。
func TestReferenceAttachmentsRejectsBothInputs(t *testing.T) {
	t.Parallel()

	_, err := referenceAttachments(Request{
		Images:     []string{"gs://bucket/a.png"},
		References: []gemini.Attachment{{URI: "gs://bucket/b.png"}},
	})

	assert.ErrorIs(t, err, ErrConflictingReferences)
}

// TestNewSeedStaysWithinInt32 は、採番したシードが gemini の受け付ける範囲に
// 収まることを検証します。範囲外は ErrInvalidSeed で弾かれます。
func TestNewSeedStaysWithinInt32(t *testing.T) {
	t.Parallel()

	for range 100 {
		got := newSeed()

		require.NotNil(t, got)
		assert.GreaterOrEqual(t, *got, int64(0))
		assert.Less(t, *got, int64(math.MaxInt32))
	}
}

func TestSeedOrZero(t *testing.T) {
	t.Parallel()

	seed := int64(42)

	assert.Equal(t, int64(42), seedOrZero(&seed))
	assert.Zero(t, seedOrZero(nil))
}

// TestRequestPromotesGenerateOptions は、埋め込みによる昇格でフィールドが
// そのまま設定できることを検証します。写し取りではなく埋め込みにしているのは、
// gemini 側の項目追加のたびに 2 か所を同期しないためです。
func TestRequestPromotesGenerateOptions(t *testing.T) {
	t.Parallel()

	req := Request{Model: "m", Prompt: "p"}
	req.SystemPrompt = "system"
	req.AspectRatio = "16:9"
	req.Temperature = new(float32(0))

	// 昇格したフィールドへの代入が、埋め込んだ値そのものに届いていること。
	opts := req.GenerateOptions
	assert.Equal(t, "system", opts.SystemPrompt)
	assert.Equal(t, "16:9", opts.AspectRatio)
	require.NotNil(t, opts.Temperature)
	assert.Zero(t, *opts.Temperature)
}

// TestPrepareForwardsPromotedOptions は、昇格したフィールドが送信オプションへ
// そのまま乗ることを検証します。
func TestPrepareForwardsPromotedOptions(t *testing.T) {
	t.Parallel()

	client := &Client{ai: &fakeGenerator{}}
	req := Request{Model: "m", Prompt: strings.Repeat("p", 3)}
	req.AspectRatio = "9:16"
	req.ImageSize = "2K"

	got, err := client.prepare(req)

	require.NoError(t, err)
	assert.Equal(t, "9:16", got.opts.AspectRatio)
	assert.Equal(t, "2K", got.opts.ImageSize)
}
