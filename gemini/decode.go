package gemini

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidJSON indicates that the model's response body could not be interpreted as JSON.
// It is a response that the repair (CleanJSONResponse) could not fix, and unless the prompt
// changes, the same input comes back broken the same way.
var ErrInvalidJSON = errors.New("gemini: response is not valid JSON")

// maxDecodeExcerpt is the maximum number of bytes of the response excerpt put in a decode-failure error.
// The full text would bloat the logs, so only the beginning, which is enough for diagnosis, is kept.
const maxDecodeExcerpt = 200

// DecodeJSON decodes the model's response body (GenerateResponse.Text) into T.
//
// Even with structured output (ResponseMIMEType application/json), the body contains code
// fences, a preamble sentence, or broken escapes inside strings. That is why it always goes
// through CleanJSONResponse before decoding; writing this by hand means that on a path that
// forgot it, a generation that took minutes is lost, leaving only a one-line "cannot be parsed".
//
// An empty body is returned as ErrEmptyResponse, and an unparsable body as ErrInvalidJSON.
// Both can be classified with errors.Is. The error message carries the start of the original response as an excerpt.
//
// It decodes with encoding/json (v1). v2 matches member names case-sensitively, so a
// response where the model returned "Title" would become a zero-value struct rather than an error.
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

// decodeExcerpt returns the start of the response for the error message. A UTF-8 sequence cut midway is dropped.
func decodeExcerpt(s string) string {
	if len(s) <= maxDecodeExcerpt {
		return s
	}
	return strings.ToValidUTF8(s[:maxDecodeExcerpt], "") + "…(truncated)"
}
