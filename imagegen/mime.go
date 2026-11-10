package imagegen

import (
	"mime"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// ExtensionByMIMEType returns the file extension, with its leading dot, for saving an image
// of the given MIME type. For an unrecognised MIME type it returns ".png", so saving itself
// never stops. A MIME type with parameters, such as the value of a Content-Type header,
// can be passed as is.
//
// It is meant for passing Response.MIMEType as is to decide where to save the output.
// The standard mime.ExtensionsByType is not used because the extension it returns varies by
// environment, depending on the OS MIME database (image/jpeg can come back as ".jpe").
// Saved paths persist in URLs and history, so the table is fixed.
//
// Falling back to a default when it cannot tell is the opposite of mimeTypeByPath, which
// defers to the caller with an empty string, but the damage differs. A MIME type declaration
// directly decides how the receiver interprets the content, so a wrong one breaks the content
// itself. An extension, on the other hand, only affects how the saved path looks, and the
// Content-Type at delivery is set separately by the storage side from the MIME type. Failing to
// save a generated image and discarding it loses more.
func ExtensionByMIMEType(mimeType string) string {
	trimmed := strings.TrimSpace(mimeType)

	// Even with broken parameters, mime.ParseMediaType returns the media type itself
	// (ErrInvalidMediaParameter). Only when it comes back empty is the raw value tried.
	mediaType, _, err := mime.ParseMediaType(trimmed)
	if err != nil && mediaType == "" {
		mediaType = strings.ToLower(trimmed)
	}

	switch mediaType {
	case "image/jpeg", "image/jpg":
		// image/jpg is not an official MIME type, but it does appear in generation model responses
		// and hand-written settings. Falling back to the default .png would make the content and the
		// extension disagree, so it is treated as a spelling variant. This is the same reason
		// mimeTypeByPath accepts both ".jpg" and ".jpeg".
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}

// mimeTypeByPath looks up the MIME type from the extension of a path or URI.
// It returns an empty string when it cannot tell (deferring to the server's content
// detection is safer than declaring the wrong type).
//
// It is the counterpart lookup of ExtensionByMIMEType. When adding a supported format,
// update both.
//
// For a URI with a query, such as a signed URL, the extension of the path part with the query
// removed is used. A plain filepath.Ext returns ".png?X-Goog-Signature=..." as the extension,
// so the most common URL shape would always be unrecognised.
func mimeTypeByPath(rawPath string) string {
	ext := strings.ToLower(filepath.Ext(rawPath))
	if u, err := url.Parse(rawPath); err == nil && u.Path != "" {
		ext = strings.ToLower(path.Ext(u.Path))
	}
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return ""
	}
}

// isGCSURI reports whether the given URI points to GCS.
// The scheme is matched case-insensitively.
func isGCSURI(uri string) bool {
	return strings.HasPrefix(strings.ToLower(uri), "gs://")
}
