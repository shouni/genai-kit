package gemini

import (
	"errors"
	"strings"
	"testing"
)

// TestAttachmentPartsBuildsTextThenInlineData checks that the prompt comes first and the
// attachments follow in order.
func TestAttachmentPartsBuildsTextThenInlineData(t *testing.T) {
	parts, err := attachmentParts("describe this", []Attachment{
		{MIMEType: "audio/mpeg", Data: []byte("song")},
		{MIMEType: "image/png", Data: []byte("cover")},
	})
	if err != nil {
		t.Fatalf("attachmentParts() error = %v", err)
	}

	if len(parts) != 3 {
		t.Fatalf("parts = %d, want 3", len(parts))
	}
	if parts[0].Text != "describe this" {
		t.Errorf("parts[0].Text = %q, want the prompt first", parts[0].Text)
	}

	want := []Attachment{
		{MIMEType: "audio/mpeg", Data: []byte("song")},
		{MIMEType: "image/png", Data: []byte("cover")},
	}
	for i, w := range want {
		blob := parts[i+1].InlineData
		if blob == nil {
			t.Fatalf("parts[%d].InlineData = nil", i+1)
		}
		if blob.MIMEType != w.MIMEType || string(blob.Data) != string(w.Data) {
			t.Errorf("parts[%d] = %q/%q, want %q/%q", i+1, blob.MIMEType, blob.Data, w.MIMEType, w.Data)
		}
	}
}

// TestAttachmentPartsSkipsEmptyAttachments checks that empty attachments are dropped.
// Callers build images as "pass it if there is one", so they are not made to strip empty elements every time.
func TestAttachmentPartsSkipsEmptyAttachments(t *testing.T) {
	parts, err := attachmentParts("prompt", []Attachment{
		{MIMEType: "image/png"},
		{MIMEType: "image/png", Data: []byte("real")},
	})
	if err != nil {
		t.Fatalf("attachmentParts() error = %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2 (empty attachments are dropped)", len(parts))
	}
}

// TestAttachmentPartsAllowsAttachmentOnlyRequests checks that a request can be sent with
// attachments alone and no prompt. There are uses that pass only audio or images to have them analysed.
func TestAttachmentPartsAllowsAttachmentOnlyRequests(t *testing.T) {
	parts, err := attachmentParts("", []Attachment{{MIMEType: "audio/mpeg", Data: []byte("song")}})
	if err != nil {
		t.Fatalf("attachmentParts() error = %v", err)
	}
	if len(parts) != 1 || parts[0].InlineData == nil {
		t.Fatalf("parts = %+v, want exactly one inline data part", parts)
	}
}

// TestAttachmentPartsRejectsEmptyRequests checks that having nothing to send is an error.
// Sending an empty request results in a hard-to-understand error from the API.
func TestAttachmentPartsRejectsEmptyRequests(t *testing.T) {
	tests := map[string][]Attachment{
		"neither prompt nor attachments": nil,
		"only empty attachments":         {{MIMEType: "image/png"}},
	}

	for name, attachments := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := attachmentParts("", attachments); !errors.Is(err, ErrEmptyParts) {
				t.Errorf("attachmentParts() error = %v, want ErrEmptyParts", err)
			}
		})
	}
}

// TestAttachmentPartsRequiresMIMETypeForInlineData checks that bytes without a MIME type
// are an error. The API cannot guess it.
func TestAttachmentPartsRequiresMIMETypeForInlineData(t *testing.T) {
	_, err := attachmentParts("prompt", []Attachment{{Data: []byte("bytes")}})

	if !errors.Is(err, ErrInvalidAttachment) {
		t.Fatalf("attachmentParts() error = %v, want ErrInvalidAttachment", err)
	}
	if errors.Is(err, ErrEmptyParts) {
		t.Error("a missing MIME type is classified as ErrEmptyParts")
	}
}

// TestAttachmentPartsBuildsFileDataForURIs checks that a URI reference becomes a FileData
// part rather than inline data. Vertex AI can read gs:// directly, so the path that passes a
// reference must be expressible with the same type as bytes.
func TestAttachmentPartsBuildsFileDataForURIs(t *testing.T) {
	parts, err := attachmentParts("describe", []Attachment{
		{URI: "gs://bucket/cover.png", MIMEType: "image/png"},
		{URI: "gs://bucket/unknown"},
	})
	if err != nil {
		t.Fatalf("attachmentParts() error = %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("parts = %d, want 3", len(parts))
	}

	if parts[1].FileData == nil || parts[1].FileData.FileURI != "gs://bucket/cover.png" {
		t.Errorf("parts[1] = %+v, want a gs:// reference", parts[1])
	}
	if parts[1].FileData.MIMEType != "image/png" {
		t.Errorf("parts[1] MIME type = %q, want it passed through as is", parts[1].FileData.MIMEType)
	}
	// A URI whose MIME type cannot be guessed is sent with it empty, leaving the decision to the server.
	if parts[2].FileData == nil || parts[2].FileData.MIMEType != "" {
		t.Errorf("parts[2] = %+v, want the MIME type left empty", parts[2])
	}
	if parts[1].InlineData != nil || parts[2].InlineData != nil {
		t.Error("a URI reference was sent as inline data")
	}
}

// TestAttachmentValidateSourceClassifiesByCallerSentinel checks that the same "Data together
// with URI" is classified into a different sentinel per call path.
//
// The definition is in one place (validateSource), but downstream errors.Is must work as
// ErrInvalidAttachment for generation input and as ErrInvalidVideoInput for video generation input.
func TestAttachmentValidateSourceClassifiesByCallerSentinel(t *testing.T) {
	both := Attachment{MIMEType: "image/png", Data: []byte("bytes"), URI: "gs://bucket/cover.png"}

	tests := []struct {
		name     string
		sentinel error
		other    error
	}{
		{"generation input", ErrInvalidAttachment, ErrInvalidVideoInput},
		{"video generation input", ErrInvalidVideoInput, ErrInvalidAttachment},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := both.validateSource(tt.sentinel, "Image")
			if !errors.Is(err, tt.sentinel) {
				t.Fatalf("validateSource() error = %v, want %v", err, tt.sentinel)
			}
			if errors.Is(err, tt.other) {
				t.Errorf("validateSource() error = %v, want it not classified as %v", err, tt.other)
			}
			if !strings.Contains(err.Error(), "Image") {
				t.Errorf("error = %q, want it to name which input was invalid", err)
			}
		})
	}

	t.Run("no error when only one is set", func(t *testing.T) {
		for _, a := range []Attachment{
			{MIMEType: "image/png", Data: []byte("bytes")},
			{URI: "gs://bucket/cover.png"},
			{},
		} {
			if err := a.validateSource(ErrInvalidAttachment, "Image"); err != nil {
				t.Errorf("validateSource(%+v) error = %v, want nil", a, err)
			}
		}
	})
}

// TestAttachmentPartsRejectsDataAndURITogether checks that setting both is refused before
// sending. Sending both would leave it to the API which one takes effect.
func TestAttachmentPartsRejectsDataAndURITogether(t *testing.T) {
	_, err := attachmentParts("prompt", []Attachment{
		{MIMEType: "image/png", Data: []byte("bytes"), URI: "gs://bucket/cover.png"},
	})

	if !errors.Is(err, ErrInvalidAttachment) {
		t.Fatalf("attachmentParts() error = %v, want ErrInvalidAttachment", err)
	}
}

// TestAttachmentIsEmptyCoversBothCarriers checks that a URI-only attachment is not treated
// as "empty". Treating it as empty would silently drop every reference-based image.
func TestAttachmentIsEmptyCoversBothCarriers(t *testing.T) {
	tests := map[string]struct {
		attachment Attachment
		want       bool
	}{
		"nothing":             {attachment: Attachment{MIMEType: "image/png"}, want: true},
		"with Data":           {attachment: Attachment{Data: []byte("x")}, want: false},
		"with URI":            {attachment: Attachment{URI: "gs://bucket/a.png"}, want: false},
		"no MIME type either": {attachment: Attachment{}, want: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.attachment.IsEmpty(); got != tt.want {
				t.Errorf("IsEmpty() = %v, want %v", got, tt.want)
			}
		})
	}
}
