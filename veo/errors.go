package veo

import "errors"

// Sentinel errors about input and results. Callers classify them with errors.Is and
// can choose a response different from a transport error (such as revising the prompt).
var (
	// ErrGeneratorRequired is returned when New is given a nil generation client.
	ErrGeneratorRequired = errors.New("veo: video generator is required")

	// ErrMissingOperationName is returned when the operation name needed to wait for
	// completion is missing. An unfinished operation always has a name, so this should not normally happen.
	ErrMissingOperationName = errors.New("veo: operation has no name to poll")

	// ErrNoVideoGenerated is returned when the operation completed successfully but not a
	// single video came back. Exclusion by a safety policy is the typical case, and the
	// reason is then included in the error message. Retrying does not fix it.
	ErrNoVideoGenerated = errors.New("veo: no video was generated")

	// ErrPollFailed is returned when checking the generation status fails repeatedly and
	// completion can no longer be waited for. The last cause can be reached through Unwrap.
	ErrPollFailed = errors.New("veo: polling for completion failed")
)
