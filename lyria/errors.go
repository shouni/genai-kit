package lyria

import (
	"errors"
	"strings"
)

// センチネルエラー。呼び出し側は errors.Is で判定し、リトライやプロンプトの
// 見直しなど失敗の種類に応じた制御を選べます。
var (
	// ErrWorkflowConfig は、New に必要な依存やモデル名が欠けている場合に返されます。
	ErrWorkflowConfig = errors.New("lyria: workflow configuration is incomplete")
	// ErrNilInput は、生成に必要な入力（収集コンテンツ・歌詞・レシピ）が nil の場合に返されます。
	ErrNilInput = errors.New("lyria: required input is nil")
	// ErrEmptyLyrics は、生成された歌詞ドラフトの本文が空だった場合に返されます。
	// プロンプトを変えずに再試行しても解決しない可能性が高い失敗です。
	ErrEmptyLyrics = errors.New("lyria: lyrics draft is empty")
	// ErrNoAudio は、Lyria の呼び出しは成功したのに音声データが返らなかった場合に返されます。
	ErrNoAudio = errors.New("lyria: no audio data received")
	// ErrInvalidResponse は、モデル出力が期待する JSON として解釈できなかった場合に
	// 返されます。再生成（リトライ）で解決することがあります。
	ErrInvalidResponse = errors.New("lyria: model response is not valid")
)

// maxErrorPayload は、エラーメッセージへ埋め込むモデル生出力の上限バイト数です。
// レシピ JSON は数 KB になるため、全文を埋め込むとログ1行が肥大化します。
const maxErrorPayload = 200

// truncateForError は、エラーメッセージに埋め込む生テキストを上限まで切り詰めます。
// UTF-8 の途中で切れた不完全なバイト列は取り除きます。
func truncateForError(s string) string {
	if len(s) <= maxErrorPayload {
		return s
	}
	return strings.ToValidUTF8(s[:maxErrorPayload], "") + "…(truncated)"
}
