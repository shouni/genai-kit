package lyria

import (
	"errors"
	"strings"
)

// Sentinel errors. Callers classify them with errors.Is and can choose a response
// suited to the kind of failure, such as retrying or revising the prompt.
var (
	// ErrWorkflowConfig is returned when a dependency or model name required by New is missing.
	ErrWorkflowConfig = errors.New("lyria: workflow configuration is incomplete")
	// ErrNilInput is returned when an input required for generation (collected content, lyrics, recipe) is nil.
	ErrNilInput = errors.New("lyria: required input is nil")
	// ErrEmptyLyrics is returned when the body of a generated lyrics draft is empty.
	// It is a failure that retrying without changing the prompt is unlikely to fix.
	ErrEmptyLyrics = errors.New("lyria: lyrics draft is empty")
	// ErrNoAudio is returned when the Lyria call succeeded but no audio data came back.
	ErrNoAudio = errors.New("lyria: no audio data received")
	// ErrInvalidResponse is returned when the model output cannot be interpreted as the
	// expected JSON. Regenerating (retrying) may fix it.
	ErrInvalidResponse = errors.New("lyria: model response is not valid")
)

// maxErrorPayload is the maximum number of bytes of raw model output embedded in an error message.
// A recipe JSON runs to several KB, so embedding all of it bloats a single log line.
const maxErrorPayload = 200

// truncateForError truncates raw text embedded in an error message to the limit.
// An incomplete byte sequence cut in the middle of UTF-8 is removed.
func truncateForError(s string) string {
	if len(s) <= maxErrorPayload {
		return s
	}
	return strings.ToValidUTF8(s[:maxErrorPayload], "") + "…(truncated)"
}
