package imagegen

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestExtensionByMIMEType checks the lookup of the saved file's extension.
//
// Falling back to the default (.png) for an unrecognised MIME type is the opposite of
// mimeTypeByPath, which defers with an empty string, because the damage differs. A wrong
// extension only affects how the saved path looks, while failing to save a generated image and discarding it loses more.
func TestExtensionByMIMEType(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"image/png":                ".png",
		"image/jpeg":               ".jpg",
		"image/webp":               ".webp",
		"image/gif":                ".gif",
		"IMAGE/PNG":                ".png",
		"  image/png  ":            ".png",
		"image/png; charset=utf-8": ".png",
		// image/jpg is not an official MIME type, but it does appear in generation model
		// responses and hand-written settings. Falling back to the default would contradict the content.
		"image/jpg": ".jpg",
		// Anything unrecognised falls back to the default.
		"application/octet-stream": ".png",
		"":                         ".png",
		"not a mime type":          ".png",
		// Even with broken parameters, the media type itself is picked up.
		"image/webp; =broken": ".webp",
	}

	for mimeType, want := range tests {
		t.Run(mimeType, func(t *testing.T) {
			assert.Equal(t, want, ExtensionByMIMEType(mimeType))
		})
	}
}

// TestMimeTypeByPath checks the lookup from a path's or URI's extension.
// It returns an empty string when it cannot tell, because a wrongly declared type directly
// breaks the receiver's interpretation.
func TestMimeTypeByPath(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"gs://bucket/a.png":  "image/png",
		"gs://bucket/a.jpg":  "image/jpeg",
		"gs://bucket/a.jpeg": "image/jpeg",
		"gs://bucket/a.webp": "image/webp",
		"gs://bucket/a.gif":  "image/gif",
		"gs://bucket/a.PNG":  "image/png",
		"a.png":              "image/png",
		"gs://bucket/a":      "",
		"gs://bucket/a.txt":  "",
		"":                   "",
		// Even with a query, as in a signed URL, the extension of the path part is used.
		// A plain filepath.Ext returns ".png?X-Goog-Signature=...", so the most common URL
		// shape would always be unrecognised.
		"https://storage.googleapis.com/bucket/a.png?X-Goog-Signature=abc": "image/png",
	}

	for path, want := range tests {
		t.Run(path, func(t *testing.T) {
			assert.Equal(t, want, mimeTypeByPath(path))
		})
	}
}

// TestMIMETablesStayInSync checks that the two tables cover the same set of formats.
// Adding a format to only one of them makes sending and saving disagree.
func TestMIMETablesStayInSync(t *testing.T) {
	t.Parallel()

	// Every MIME type mimeTypeByPath can return must be accepted by ExtensionByMIMEType
	// with an extension other than the default.
	pathToMIME := map[string]string{
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".webp": "image/webp",
		".gif":  "image/gif",
	}

	for ext, mimeType := range pathToMIME {
		assert.Equal(t, mimeType, mimeTypeByPath("gs://bucket/a"+ext))
		if ext == ".jpg" {
			assert.Equal(t, ".jpg", ExtensionByMIMEType(mimeType))
			continue
		}
		assert.Equal(t, ext, ExtensionByMIMEType(mimeType))
	}
}

func TestIsGCSURI(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"gs://bucket/a.png":         true,
		"GS://bucket/a.png":         true,
		"Gs://bucket/a.png":         true,
		"https://example.com/a.png": false,
		"bucket/a.png":              false,
		"":                          false,
	}

	for uri, want := range tests {
		t.Run(uri, func(t *testing.T) {
			assert.Equal(t, want, isGCSURI(uri))
		})
	}
}
