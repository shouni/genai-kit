package gemini

import (
	"errors"
	"testing"

	"google.golang.org/genai"
)

func TestAPIResponseErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		err  *APIResponseError
		want string
	}{
		{
			name: "Message を優先する",
			err:  &APIResponseError{Reason: ErrBlocked, Message: "明示メッセージ"},
			want: "明示メッセージ",
		},
		{
			name: "Message が空なら FinishReason から組み立てる",
			err:  &APIResponseError{Reason: ErrBlocked, FinishReason: genai.FinishReasonSafety},
			want: "生成がブロックされました（理由: SAFETY）",
		},
		{
			name: "Message も FinishReason も無ければ Reason を返す",
			err:  &APIResponseError{Reason: ErrEmptyResponse},
			want: ErrEmptyResponse.Error(),
		},
		{
			// ゼロ値の FinishReason を「理由あり」と誤認すると、空の理由が文面に出ます。
			name: "FinishReason がゼロ値なら理由として扱わない",
			err:  &APIResponseError{Reason: ErrEmptyResponse, FinishReason: ""},
			want: ErrEmptyResponse.Error(),
		},
		{
			name: "何も無ければ既定メッセージ",
			err:  &APIResponseError{},
			want: "gemini: API response error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAPIResponseErrorUnwrap は、Unwrap が分類用センチネルを返し、
// errors.Is / errors.AsType の両方で判定できることを検証します。
// 公開のエラー契約はこの形で使われる前提です。
func TestAPIResponseErrorUnwrap(t *testing.T) {
	t.Run("Reason で分類できること", func(t *testing.T) {
		err := newBlockedError(genai.FinishReasonRecitation)

		if !errors.Is(err, ErrBlocked) {
			t.Error("ErrBlocked に一致しません")
		}
		if errors.Is(err, ErrEmptyResponse) {
			t.Error("ErrEmptyResponse に誤って一致しています")
		}
		apiErr, ok := errors.AsType[*APIResponseError](err)
		if !ok {
			t.Fatalf("error type = %T, want *APIResponseError", err)
		}
		if apiErr.FinishReason != genai.FinishReasonRecitation {
			t.Errorf("FinishReason = %v", apiErr.FinishReason)
		}
	})

	t.Run("空レスポンスは ErrEmptyResponse に分類されること", func(t *testing.T) {
		err := newEmptyResponseError()

		if !errors.Is(err, ErrEmptyResponse) {
			t.Error("ErrEmptyResponse に一致しません")
		}
		if !isUnsetFinishReason(err.FinishReason) {
			t.Errorf("FinishReason = %q, want 未設定", err.FinishReason)
		}
	})

	t.Run("Reason が nil でも panic しないこと", func(t *testing.T) {
		if errors.Unwrap(&APIResponseError{Message: "x"}) != nil {
			t.Error("Reason が nil なら Unwrap は nil を返すべきです")
		}
	})
}

func TestIsUnsetFinishReason(t *testing.T) {
	tests := map[genai.FinishReason]bool{
		"":                            true,
		genai.FinishReasonUnspecified: true,
		genai.FinishReasonStop:        false,
		genai.FinishReasonSafety:      false,
		genai.FinishReasonMaxTokens:   false,
	}

	for reason, want := range tests {
		if got := isUnsetFinishReason(reason); got != want {
			t.Errorf("isUnsetFinishReason(%q) = %v, want %v", reason, got, want)
		}
	}
}
