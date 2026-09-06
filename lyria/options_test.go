package lyria

import (
	"testing"
	"time"

	"github.com/shouni/genai-kit/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyOptions(t *testing.T) {
	t.Parallel()

	t.Run("指定した値が反映されること", func(t *testing.T) {
		got := applyOptions(
			WithGeminiModel("gemini-flash"),
			WithLyriaModel("lyria-3"),
			WithRateInterval(250*time.Millisecond),
			WithTextRateInterval(100*time.Millisecond),
			WithExecTimeout(90*time.Second),
		)

		assert.Equal(t, "gemini-flash", got.geminiModel)
		assert.Equal(t, "lyria-3", got.lyriaModel)
		assert.Equal(t, 250*time.Millisecond, got.rateInterval)
		assert.Equal(t, 100*time.Millisecond, got.textRateInterval)
		assert.Equal(t, 90*time.Second, got.execTimeout)
	})

	// execTimeout の 0 は「未設定」で、callguard 側の既定値へ倒れます。
	// ここで既定値を埋めてしまうと、callguard の既定と二重管理になります。
	t.Run("未指定はゼロ値のままにすること", func(t *testing.T) {
		got := applyOptions()

		assert.Zero(t, got.execTimeout)
		assert.Zero(t, got.rateInterval)
		assert.Zero(t, got.textRateInterval)
	})

	t.Run("nil の Option を読み飛ばすこと", func(t *testing.T) {
		got := applyOptions(nil, WithGeminiModel("g"), nil)

		assert.Equal(t, "g", got.geminiModel)
	})
}

// TestJSONOptions は、構造化出力の設定が揃うことを検証します。
// スキーマが落ちるとモデル出力が文法レベルで制約されなくなります。
func TestJSONOptions(t *testing.T) {
	t.Parallel()

	schema := lyricsDraftSchema()
	seed := int64(42)

	got := jsonOptions(&seed, schema)

	assert.Equal(t, "application/json", got.ResponseMIMEType)
	assert.Same(t, schema, got.ResponseSchema)
	if assert.NotNil(t, got.Seed) {
		assert.Equal(t, seed, *got.Seed)
	}
}

// TestAudioOptions は、Lyria 向けのオプションを検証します。
// Lyria はレスポンス MIME type の指定なしで音声を返すため、指定しません。
func TestAudioOptions(t *testing.T) {
	t.Parallel()

	t.Run("seed が nil なら渡さないこと", func(t *testing.T) {
		got := audioOptions(nil)

		assert.Nil(t, got.Seed)
		assert.Empty(t, got.ResponseMIMEType)
	})

	t.Run("seed を保つこと", func(t *testing.T) {
		seed := int64(42)

		got := audioOptions(&seed)

		require.NotNil(t, got.Seed)
		assert.Equal(t, seed, *got.Seed)
		assert.Empty(t, got.ResponseMIMEType)
	})
}

// TestBaseOptionsUsesBlockNone は、共通の安全設定が BLOCK_NONE で揃うことを
// 検証します。生成結果の再現性を優先した選択で、入出力の制御は呼び出し側または
// 後段処理が受け持つ前提です。
func TestBaseOptionsUsesBlockNone(t *testing.T) {
	t.Parallel()

	got := baseOptions(nil, "")

	require.NotEmpty(t, got.SafetySettings)
	for _, setting := range got.SafetySettings {
		assert.Equal(t, gemini.SafetyBlockNone, setting.Threshold)
	}
}
