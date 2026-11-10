package imagegen

import "errors"

// Sentinel text is English with an "imagegen: " prefix. That way the originating package can
// be told even when the error is buried deep in a wrap; human-facing context is added by the
// wrapping side (fmt.Errorf with %w).
var (
	// ErrGeneratorRequired is returned when New is not given a generation client.
	ErrGeneratorRequired = errors.New("imagegen: gemini generator is required")
	// ErrModelRequired is returned when a generation request does not specify a model name.
	ErrModelRequired = errors.New("imagegen: model is required")
	// ErrEmptyPrompt is returned when the prompt (including the negative prompt) is empty.
	ErrEmptyPrompt = errors.New("imagegen: prompt cannot be empty")
	// ErrUnsupportedReference is returned when a reference image URI points to something other than gs://.
	//
	// Only gs://, which Vertex AI can resolve on the model side, can be referenced by URI.
	// For an http(s):// image, have the caller fetch it and pass the bytes in
	// Request.References.
	ErrUnsupportedReference = errors.New("imagegen: reference URI must be a gs:// URI")
	// ErrConflictingReferences is returned when both Request.Images and Request.References
	// are specified. There is no basis for ordering the two lists against each other, and the
	// order of reference images changes the result, so they are refused rather than silently joined.
	ErrConflictingReferences = errors.New("imagegen: Images and References cannot both be set")
	// ErrMissingReferenceMIMEType is returned when a reference image passed as bytes has no
	// MIME type. The type cannot be determined from the bytes, and a wrong declaration breaks
	// the receiver's interpretation, so it is required rather than guessed.
	ErrMissingReferenceMIMEType = errors.New("imagegen: inline reference requires a MIME type")
	// ErrNoImageData is returned when the response contains no image data.
	ErrNoImageData = errors.New("imagegen: no image data found in response")
)
