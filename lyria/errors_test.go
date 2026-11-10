package lyria

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTruncateForError checks the truncation of raw output embedded in error messages.
// A recipe JSON runs to several KB, so embedding all of it bloats a single log line.
func TestTruncateForError(t *testing.T) {
	t.Parallel()

	t.Run("returns the input unchanged at or below the limit", func(t *testing.T) {
		short := strings.Repeat("a", maxErrorPayload)

		assert.Equal(t, short, truncateForError(short))
	})

	t.Run("truncates beyond the limit", func(t *testing.T) {
		long := strings.Repeat("a", maxErrorPayload+100)

		got := truncateForError(long)

		assert.True(t, strings.HasSuffix(got, "…(truncated)"))
		assert.Less(t, len(got), len(long))
	})

	// Cutting in the middle of a UTF-8 sequence leaves invalid bytes, which log ingestion may reject.
	t.Run("leaves no invalid bytes when cutting inside a multibyte character", func(t *testing.T) {
		long := strings.Repeat("あ", maxErrorPayload)

		got := truncateForError(long)

		assert.True(t, strings.HasSuffix(got, "…(truncated)"))
		assert.True(t, isValidUTF8(got), "invalid UTF-8 remains: %q", got)
	})
}

func isValidUTF8(s string) bool {
	return strings.ToValidUTF8(s, "�") == s
}
