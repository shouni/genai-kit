package imagegen

import (
	"fmt"

	"github.com/shouni/genai-kit/gemini"
)

// Response is the generated image data and its metadata.
type Response struct {
	// Data is the bytes of the generated image. When OutputURIPrefix is specified it is empty,
	// and URI holds the destination instead.
	Data []byte
	// URI is the gs:// URI the API wrote the image to when OutputURIPrefix was specified.
	// If not specified it is empty, and Data holds the image.
	URI      string
	MIMEType string
	// UsedSeed is the seed specified in the request (or picked automatically).
	// The API does not return the seed in the response, so this is a record of the value sent.
	UsedSeed int64
	// Model is the model name used for generation. It is included in the response so that callers
	// do not have to carry it separately from the request for cost accounting or metadata storage.
	Model string
	// Prompt is the final prompt actually sent (with the negative prompt joined in).
	Prompt string
	// Usage is the token usage. It is nil when the model does not return it.
	Usage *gemini.TokenUsage
}

// toResponse extracts the image data from the response.
//
// Checking FinishReason (blocks by a safety filter, etc.) is done by gemini, and on a block the
// generation call itself returns an error, so it is not done here. The only thing this package
// distinguishes is "no image data".
func (p preparedRequest) toResponse(resp *gemini.Response) (*Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("%w: no response", ErrNoImageData)
	}

	// gemini.Response.Attachments come back with their MIME type, so there is no need to walk the
	// raw SDK response to decide the Content-Type when saving.
	for _, attachment := range resp.Attachments {
		if attachment.IsEmpty() {
			continue
		}
		mimeType := attachment.MIMEType
		if mimeType == "" && attachment.URI != "" {
			// Only when the server did not attach a type, fill it in from the destination's extension.
			// If that is unknown too, return it empty (see mimeTypeByPath for why).
			mimeType = mimeTypeByPath(attachment.URI)
		}
		return &Response{
			Data:     attachment.Data,
			URI:      attachment.URI,
			MIMEType: mimeType,
			UsedSeed: seedOrZero(p.opts.Seed),
			Model:    p.model,
			Prompt:   p.prompt,
			Usage:    resp.Usage,
		}, nil
	}

	return nil, fmt.Errorf("%w: response contains no image", ErrNoImageData)
}
