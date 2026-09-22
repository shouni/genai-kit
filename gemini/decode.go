package gemini

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidJSON は、モデルの応答本文を JSON として解釈できなかったことを示します。
// 補修（CleanJSONResponse）を通しても直らなかった応答で、プロンプトを変えない限り
// 同じ入力からは同じ壊れ方が返ります。
var ErrInvalidJSON = errors.New("gemini: response is not valid JSON")

// maxDecodeExcerpt は、デコード失敗のエラー文に載せる応答抜粋の上限バイト数です。
// 全文はログを肥大化させるため、診断に足りる先頭だけを残します。
const maxDecodeExcerpt = 200

// DecodeJSON は、モデルの応答本文（GenerateResponse.Text）を T にデコードします。
//
// 構造化出力（ResponseMIMEType が application/json）でも、本文にはコードフェンスや
// 前置きの文、文字列の中の壊れたエスケープが混ざります。デコードの前に必ず
// CleanJSONResponse を通すのはそのためで、ここを手書きすると通し忘れた経路で
// 数分かけた生成が「解釈できません」の一行だけを残して失われます。
//
// 空の本文は ErrEmptyResponse、解釈できない本文は ErrInvalidJSON で返します。
// どちらも errors.Is で判定できます。エラー文には元の応答の先頭を抜粋として載せます。
//
// encoding/json（v1）でデコードします。v2 はメンバー名を大文字小文字まで区別するため、
// モデルが "Title" と返した応答がエラーではなくゼロ値の構造体になります。
func DecodeJSON[T any](text string) (T, error) {
	var out T

	raw := strings.TrimSpace(text)
	if raw == "" {
		return out, fmt.Errorf("%w: response text is empty", ErrEmptyResponse)
	}

	if err := json.Unmarshal([]byte(CleanJSONResponse(raw)), &out); err != nil {
		var zero T
		return zero, fmt.Errorf("%w: %w (excerpt: %s)", ErrInvalidJSON, err, decodeExcerpt(raw))
	}
	return out, nil
}

// decodeExcerpt は、エラー文に載せる応答の先頭を返します。途中で切った UTF-8 は落とします。
func decodeExcerpt(s string) string {
	if len(s) <= maxDecodeExcerpt {
		return s
	}
	return strings.ToValidUTF8(s[:maxDecodeExcerpt], "") + "…(truncated)"
}
