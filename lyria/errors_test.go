package lyria

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTruncateForError は、エラーメッセージへ埋め込む生出力の切り詰めを検証します。
// レシピ JSON は数 KB になるため、全文を埋め込むとログ 1 行が肥大化します。
func TestTruncateForError(t *testing.T) {
	t.Parallel()

	t.Run("上限以下はそのまま返すこと", func(t *testing.T) {
		short := strings.Repeat("a", maxErrorPayload)

		assert.Equal(t, short, truncateForError(short))
	})

	t.Run("上限を超えたら切り詰めること", func(t *testing.T) {
		long := strings.Repeat("a", maxErrorPayload+100)

		got := truncateForError(long)

		assert.True(t, strings.HasSuffix(got, "…(truncated)"))
		assert.Less(t, len(got), len(long))
	})

	// UTF-8 の途中で切ると不正なバイト列が残り、ログの取り込みで弾かれることがあります。
	t.Run("マルチバイト文字の途中で切っても不正なバイト列を残さないこと", func(t *testing.T) {
		long := strings.Repeat("あ", maxErrorPayload)

		got := truncateForError(long)

		assert.True(t, strings.HasSuffix(got, "…(truncated)"))
		assert.True(t, isValidUTF8(got), "不正な UTF-8 が残っています: %q", got)
	})
}

func isValidUTF8(s string) bool {
	return strings.ToValidUTF8(s, "�") == s
}
