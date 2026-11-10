package gemini

import (
	"fmt"

	"google.golang.org/genai"
)

// Attachment is one binary input attached to a prompt.
//
// It represents data passed inline to the model, such as images, audio or PDFs. Using it instead
// of building genai.Part or genai.Blob lets callers avoid importing the genai SDK.
// Multimodal input needs nothing beyond "a MIME type and bytes", so there is no reason to
// expose the SDK's types.
type Attachment struct {
	// MIMEType is the kind of data, such as "image/png" or "audio/mpeg".
	// Required when sending Data; optional when referencing a URI, in which case omitting it leaves the decision to the server.
	MIMEType string
	// Data is the bytes sent inline. Mutually exclusive with URI.
	Data []byte
	// URI is the location of a file referenced instead of sending bytes. Mutually exclusive with Data.
	// On Vertex AI, a GCS gs:// URI can be referenced directly.
	//
	// Referencing by URI keeps the request lighter than sending the same image over and over.
	// Which to use depends on the caller's circumstances, so both can be expressed with the
	// same Attachment.
	URI string
}

// IsEmpty reports whether the attachment has nothing to send.
func (a Attachment) IsEmpty() bool {
	return len(a.Data) == 0 && a.URI == ""
}

// validateSource checks that the ways of specifying the data (Data / URI) are mutually exclusive.
// field is a label that shows in the error which input was invalid.
//
// sentinel is taken as an argument because the same "Data together with URI" is classified
// differently from the caller's point of view (ErrInvalidAttachment for generation input,
// ErrInvalidVideoInput for video generation input). The wording and the condition are kept
// in one place while classification with errors.Is is preserved per path.
func (a Attachment) validateSource(sentinel error, field string) error {
	if len(a.Data) > 0 && a.URI != "" {
		return fmt.Errorf("%w: %s must set exactly one of Data and URI", sentinel, field)
	}
	return nil
}

// attachmentParts converts the prompt and attachments into a slice of genai Parts.
//
// Attachments with empty data are dropped so that callers building images as "pass it if
// there is one" do not have to strip empty elements every time. If no Part remains after
// dropping, it returns an error rather than sending an empty request.
func attachmentParts(prompt string, attachments []Attachment) ([]*genai.Part, error) {
	parts := make([]*genai.Part, 0, len(attachments)+1)
	if prompt != "" {
		parts = append(parts, &genai.Part{Text: prompt})
	}

	for i, attachment := range attachments {
		if attachment.IsEmpty() {
			continue
		}
		if err := attachment.validateSource(ErrInvalidAttachment, fmt.Sprintf("attachments[%d]", i)); err != nil {
			return nil, err
		}

		// A URI reference can omit the MIME type. Declaring the wrong type for a URL whose type cannot
		// be guessed from its extension can make decoding fail on the server, so leave room to defer to it.
		if attachment.URI != "" {
			parts = append(parts, &genai.Part{
				FileData: &genai.FileData{FileURI: attachment.URI, MIMEType: attachment.MIMEType},
			})
			continue
		}

		if attachment.MIMEType == "" {
			return nil, fmt.Errorf("%w: attachments[%d] has no MIME type", ErrInvalidAttachment, i)
		}
		parts = append(parts, &genai.Part{
			InlineData: &genai.Blob{MIMEType: attachment.MIMEType, Data: attachment.Data},
		})
	}

	if len(parts) == 0 {
		return nil, ErrEmptyParts
	}
	return parts, nil
}
