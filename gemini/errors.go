package gemini

import (
	"errors"
	"fmt"

	"google.golang.org/genai"
)

// Input validation errors.
//
// Sentinel text is English with a "gemini: " prefix. That way the originating package can
// be told even when the error is buried deep in a wrap; human-facing context is added by the
// wrapping side (fmt.Errorf with %w).
var (
	// ErrEmptyPrompt is returned when the prompt is empty.
	ErrEmptyPrompt = errors.New("gemini: prompt is empty")
	// ErrEmptyModelName is returned when the model name is empty.
	ErrEmptyModelName = errors.New("gemini: model name is empty")
	// ErrEmptyParts is returned when the generation parts are empty.
	ErrEmptyParts = errors.New("gemini: generation parts are empty")
	// ErrInvalidPart is returned when the generation parts contain nil.
	ErrInvalidPart = errors.New("gemini: generation parts must not contain nil")
	// ErrInvalidSeed is returned when Seed is outside the int32 range.
	ErrInvalidSeed = errors.New("gemini: seed must fit in int32")
	// ErrInvalidAttachment is returned when an attachment is specified incorrectly.
	// This covers setting both Data and URI, and Data without a MIME type.
	ErrInvalidAttachment = errors.New("gemini: invalid attachment")
	// ErrEmptyOperationName is returned when the operation name is empty.
	ErrEmptyOperationName = errors.New("gemini: operation name is empty")
	// ErrInvalidVideoInput is returned when the combination of video generation inputs
	// is one the API does not accept (such as Image together with Video).
	ErrInvalidVideoInput = errors.New("gemini: invalid video generation input")
	// ErrUnsupportedOption is returned when InteractionsGenerator receives GenerateOptions
	// that cannot be mapped onto Interactions. Dropping them silently would bill a generation
	// with a setting the caller thought was applied having no effect, so it stops before sending.
	ErrUnsupportedOption = errors.New("gemini: option not supported by interactions")
	// ErrInvalidOutputURIPrefix is returned when GenerateOptions.OutputURIPrefix does not
	// start with gs://. GCS is the only destination the API accepts.
	ErrInvalidOutputURIPrefix = errors.New("gemini: output URI prefix must be a gs:// URI")
)

// ErrVideoGenerationFailed indicates that a video generation operation completed as a
// failure. Fetching the operation itself succeeded, so it is distinct from a transport error.
// It is returned on VideoOperation.Failure.
var ErrVideoGenerationFailed = errors.New("gemini: video generation failed")

// Sentinel errors for when communication with the API succeeded but the response content is unusable.
// All of them are HTTP 200, so they are not covered by the SDK's built-in retries either.
var (
	// ErrBlocked indicates that generation was blocked, by a safety filter or the like.
	// Unless the prompt changes, retrying gives the same result.
	// See errors.AsType[*APIResponseError] for the detailed reason. When the input itself was
	// refused it is in BlockReason; when generation was stopped midway it is in FinishReason.
	ErrBlocked = errors.New("gemini: generation blocked")

	// ErrTruncated indicates that generation was cut off at the output token limit (MAX_TOKENS),
	// or was returned as a partial result awaiting a continuation token (CONTINUATION).
	// The remedy differs from a block: rather than changing the prompt, raise MaxOutputTokens
	// or add an instruction to shorten the output. The returned body is incomplete and cannot be used as is.
	ErrTruncated = errors.New("gemini: generation truncated")

	// ErrEmptyResponse indicates that a response came back with no candidates at all, without the
	// input having been blocked (in DecodeJSON, also when the body is empty).
	ErrEmptyResponse = errors.New("gemini: empty response")
)

// APIResponseError represents a logical error that occurred after communication with the API
// succeeded, such as blocked content or an empty response.
//
// It can be compared with ErrBlocked / ErrEmptyResponse via errors.Is.
//
//	if errors.Is(err, gemini.ErrBlocked) {
//	    // revise the prompt
//	}
//	if apiErr, ok := errors.AsType[*gemini.APIResponseError](err); ok {
//	    slog.Warn("blocked", "reason", apiErr.FinishReason)
//	}
type APIResponseError struct {
	// Reason is the sentinel used for classification (ErrBlocked / ErrTruncated / ErrEmptyResponse).
	Reason error
	// FinishReason is the finish reason the model returned when generation was stopped midway.
	// When the input was blocked or the response is empty, it is the zero value (empty string).
	FinishReason genai.FinishReason
	// BlockReason is the reason the input (prompt) itself was refused.
	// In that case no candidates are returned and FinishReason is the zero value.
	BlockReason genai.BlockedReason
	// Message is a human-readable description.
	Message string
}

func (e *APIResponseError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if !isUnsetFinishReason(e.FinishReason) {
		return fmt.Sprintf("generation was blocked (reason: %v)", e.FinishReason)
	}
	if e.Reason != nil {
		return e.Reason.Error()
	}
	return "gemini: API response error"
}

// Unwrap returns the classification sentinel, enabling classification with errors.Is.
func (e *APIResponseError) Unwrap() error { return e.Reason }

// isUnsetFinishReason reports whether the finish reason is "not set".
//
// genai.FinishReason is a string type whose Go zero value is "", but the SDK constant
// FinishReasonUnspecified is a different value, "FINISH_REASON_UNSPECIFIED".
// The server sometimes returns a response with no finish reason, which yields the zero value,
// so unless both are treated as "unset", a normal response is misjudged as blocked.
func isUnsetFinishReason(r genai.FinishReason) bool {
	return r == "" || r == genai.FinishReasonUnspecified
}

// isBlockedFinishReason reports whether the finish reason indicates an abnormal end (a block, etc.).
// MAX_TOKENS and CONTINUATION are true here too; newFinishReasonError does the classification split.
func isBlockedFinishReason(r genai.FinishReason) bool {
	return !isUnsetFinishReason(r) && r != genai.FinishReasonStop
}

// newFinishReasonError classifies an abnormal finish reason as an error.
//
// MAX_TOKENS and CONTINUATION are ErrTruncated. Mixing them into the same "blocked" as safety
// filters would give the notification the same heading even though the remedy (fix the prompt /
// raise the output limit) differs.
// Since genai v1.74.0 the SDK consumes CONTINUATION through automatic continuation, so it only
// reaches here when AutomaticContinuation is off, or when a partial result remains after continuing.
// Either way the body is incomplete, and it is not a block.
// When a hop in the middle of a continuation is refused, the SDK clears CONTINUATION, so it
// becomes ErrBlocked through extractText's PromptFeedback check rather than here.
func newFinishReasonError(reason genai.FinishReason) *APIResponseError {
	switch reason {
	case genai.FinishReasonMaxTokens:
		return &APIResponseError{
			Reason:       ErrTruncated,
			FinishReason: reason,
			Message:      "generation was cut off at the output token limit (reason: MAX_TOKENS)",
		}
	case genai.FinishReasonContinuation:
		return &APIResponseError{
			Reason:       ErrTruncated,
			FinishReason: reason,
			Message:      "generation was returned as a partial result awaiting continuation (reason: CONTINUATION)",
		}
	}
	return &APIResponseError{
		Reason:       ErrBlocked,
		FinishReason: reason,
		Message:      fmt.Sprintf("generation was blocked (reason: %v)", reason),
	}
}

// newPromptBlockedError builds the error for when the input itself was refused.
// It arrives as zero candidates with PromptFeedback.BlockReason set; without looking at it, the
// failure is reported as an "empty response" with no reason, and it is not conveyed that retrying is pointless.
func newPromptBlockedError(feedback *genai.GenerateContentResponsePromptFeedback) *APIResponseError {
	msg := fmt.Sprintf("input was blocked (reason: %v)", feedback.BlockReason)
	if feedback.BlockReasonMessage != "" {
		msg += ": " + feedback.BlockReasonMessage
	}
	return &APIResponseError{
		Reason:      ErrBlocked,
		BlockReason: feedback.BlockReason,
		Message:     msg,
	}
}

// isPromptBlocked reports whether PromptFeedback indicates that the input was blocked.
func isPromptBlocked(feedback *genai.GenerateContentResponsePromptFeedback) bool {
	return feedback != nil && feedback.BlockReason != "" && feedback.BlockReason != genai.BlockedReasonUnspecified
}

// newEmptyResponseError builds an empty-response error.
func newEmptyResponseError() *APIResponseError {
	return &APIResponseError{
		Reason:  ErrEmptyResponse,
		Message: "Vertex AI returned an empty response",
	}
}
