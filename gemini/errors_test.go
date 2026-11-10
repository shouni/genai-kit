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
			name: "prefers Message",
			err:  &APIResponseError{Reason: ErrBlocked, Message: "explicit message"},
			want: "explicit message",
		},
		{
			name: "builds from FinishReason when Message is empty",
			err:  &APIResponseError{Reason: ErrBlocked, FinishReason: genai.FinishReasonSafety},
			want: "generation was blocked (reason: SAFETY)",
		},
		{
			name: "returns Reason when neither Message nor FinishReason is set",
			err:  &APIResponseError{Reason: ErrEmptyResponse},
			want: ErrEmptyResponse.Error(),
		},
		{
			// Mistaking a zero-value FinishReason for "has a reason" puts an empty reason in the text.
			name: "does not treat a zero-value FinishReason as a reason",
			err:  &APIResponseError{Reason: ErrEmptyResponse, FinishReason: ""},
			want: ErrEmptyResponse.Error(),
		},
		{
			name: "default message when nothing is set",
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

// TestAPIResponseErrorUnwrap checks that Unwrap returns the classification sentinel, and
// that it can be classified with both errors.Is and errors.AsType.
// The public error contract assumes it is used this way.
func TestAPIResponseErrorUnwrap(t *testing.T) {
	t.Run("can be classified by Reason", func(t *testing.T) {
		err := newFinishReasonError(genai.FinishReasonRecitation)

		if !errors.Is(err, ErrBlocked) {
			t.Error("does not match ErrBlocked")
		}
		if errors.Is(err, ErrEmptyResponse) {
			t.Error("wrongly matches ErrEmptyResponse")
		}
		apiErr, ok := errors.AsType[*APIResponseError](err)
		if !ok {
			t.Fatalf("error type = %T, want *APIResponseError", err)
		}
		if apiErr.FinishReason != genai.FinishReasonRecitation {
			t.Errorf("FinishReason = %v", apiErr.FinishReason)
		}
	})

	t.Run("an empty response is classified as ErrEmptyResponse", func(t *testing.T) {
		err := newEmptyResponseError()

		if !errors.Is(err, ErrEmptyResponse) {
			t.Error("does not match ErrEmptyResponse")
		}
		if !isUnsetFinishReason(err.FinishReason) {
			t.Errorf("FinishReason = %q, want unset", err.FinishReason)
		}
	})

	t.Run("does not panic when Reason is nil", func(t *testing.T) {
		if errors.Unwrap(&APIResponseError{Message: "x"}) != nil {
			t.Error("Unwrap should return nil when Reason is nil")
		}
	})
}

func TestIsUnsetFinishReason(t *testing.T) {
	tests := map[genai.FinishReason]bool{
		"":                             true,
		genai.FinishReasonUnspecified:  true,
		genai.FinishReasonStop:         false,
		genai.FinishReasonSafety:       false,
		genai.FinishReasonMaxTokens:    false,
		genai.FinishReasonContinuation: false,
	}

	for reason, want := range tests {
		if got := isUnsetFinishReason(reason); got != want {
			t.Errorf("isUnsetFinishReason(%q) = %v, want %v", reason, got, want)
		}
	}
}
