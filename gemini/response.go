package gemini

import (
	"strings"

	"google.golang.org/genai"
)

// Response wraps a generation result.
type Response struct {
	Text   string
	Images [][]byte // holds generated images (InlineData)
	Audios [][]byte // audio data from Lyria 3 and the like
	// Attachments holds the returned media together with their MIME types.
	//
	// Images / Audios are bytes only, so for callers that need to decide the extension or
	// Content-Type when saving, this provides a form that can be extracted with its type intact.
	// The order is the order the API returned them in.
	//
	// When GenerateOptions.OutputURIPrefix is specified, the media is written to GCS and only the
	// URI is returned, so such an element has an empty Data and a URI set. Images / Audios are the
	// subset of elements that carry bytes, so URI elements are not included.
	Attachments []Attachment
	// Thoughts is the thought summary. It is set only when GenerateOptions.IncludeThoughts is true
	// and the model returned a thought summary.
	// It is not included in Text.
	Thoughts string
	Usage    *TokenUsage
}

// TokenUsage is the token usage of a generation response.
type TokenUsage struct {
	PromptTokenCount     int32
	CandidatesTokenCount int32
	TotalTokenCount      int32
	// ThoughtsTokenCount is the number of tokens consumed by thinking.
	// It is billed, so monitor this value when using thinking.
	ThoughtsTokenCount int32
}

// responseFromGenAI converts a genai response into the package's public Response type.
//
// outputToGCS is whether the request sent a GenerationOutputConfig (OutputURIPrefix).
// FileData URIs are included in Attachments only when it was sent (see extractAttachments for why).
func responseFromGenAI(resp *genai.GenerateContentResponse, outputToGCS bool) (*Response, error) {
	text, err := extractText(resp)
	if err != nil {
		return nil, err
	}

	// Sort by MIME type. Images / Audios are subsets of Attachments, an entry point for callers
	// that only want bytes without caring about the type.
	attachments := extractAttachments(resp, outputToGCS)
	var images [][]byte
	var audios [][]byte
	for _, attachment := range attachments {
		switch {
		case len(attachment.Data) == 0:
			// An element returned as a URI has no bytes.
		case strings.HasPrefix(attachment.MIMEType, "image/"):
			images = append(images, attachment.Data)
		case strings.HasPrefix(attachment.MIMEType, "audio/"):
			audios = append(audios, attachment.Data)
		}
	}

	return &Response{
		Text:        text,
		Images:      images,
		Audios:      audios,
		Attachments: attachments,
		Thoughts:    extractThoughts(resp),
		Usage:       tokenUsageFromMetadata(resp.UsageMetadata),
	}, nil
}

// firstCandidate returns the first candidate of the response, or nil if there are none.
//
// Like parts and content, the candidate slot itself is a value from the server and can be nil.
// If each extraction function for text, thoughts and inline data wrote its own nil check, gaps in
// the checks would differ per function (they actually did), so the entry to the traversal is gathered here.
func firstCandidate(resp *genai.GenerateContentResponse) *genai.Candidate {
	if resp == nil || len(resp.Candidates) == 0 {
		return nil
	}
	return resp.Candidates[0]
}

// candidateParts returns the candidate's parts. It is nil if the candidate or content is nil.
func candidateParts(candidate *genai.Candidate) []*genai.Part {
	if candidate == nil || candidate.Content == nil {
		return nil
	}
	return candidate.Content.Parts
}

// extractText extracts the text from resp.
func extractText(resp *genai.GenerateContentResponse) (string, error) {
	// An input block is checked before whether there are candidates. It normally arrives with zero
	// candidates, but when a hop in the middle of automatic continuation is refused, the SDK (genai
	// v1.75.0 and later) clears CONTINUATION from the earlier hop's candidate and keeps only the
	// PromptFeedback, so checking candidates first would return the partial body as a normal finish.
	if resp != nil && isPromptBlocked(resp.PromptFeedback) {
		return "", newPromptBlockedError(resp.PromptFeedback)
	}

	candidate := firstCandidate(resp)
	if candidate == nil {
		return "", newEmptyResponseError()
	}

	// A FinishReason other than normal (unset or STOP) is treated as having been stopped midway.
	// The unset check is gathered in isUnsetFinishReason (because the zero value and the SDK constant differ).
	if isBlockedFinishReason(candidate.FinishReason) {
		return "", newFinishReasonError(candidate.FinishReason)
	}

	// Concatenate and return all text parts.
	//
	// Models with thinking (ThinkingBudget) enabled return the thought summary as text parts with
	// Thought=true before the body. An implementation returning the first non-empty text would
	// return the thought summary instead of the body, so Thought parts are excluded.
	// Models also sometimes split the body across several parts, so concatenation is needed.
	var sb strings.Builder
	for _, part := range candidateParts(candidate) {
		if part == nil || part.Thought || part.Text == "" {
			continue
		}
		sb.WriteString(part.Text)
	}

	return sb.String(), nil
}

// extractThoughts concatenates and returns the thought summary (text parts with Thought=true).
// It is an empty string when thinking is disabled or no thought summary is returned.
func extractThoughts(resp *genai.GenerateContentResponse) string {
	var sb strings.Builder
	for _, part := range candidateParts(firstCandidate(resp)) {
		if part == nil || !part.Thought || part.Text == "" {
			continue
		}
		sb.WriteString(part.Text)
	}
	return sb.String()
}

// extractAttachments extracts the media in the response with their MIME types, in the order
// they were returned.
//
// When withFileData is true, it picks up FileData in addition to inline data.
// With OutputURIPrefix specified, the media is written to GCS and only a FileData URI comes
// back, so looking only at InlineData would miss the output entirely.
//
// FileData is not picked up otherwise because callers read on the premise that "every element of
// Attachments has bytes". If unexpected FileData slipped in as elements with an empty Data, a
// response that used to be a no-data error would pass as a success with empty bytes
// (imagegen would return an empty image, lyria an empty Track, and the storage side would write an empty file).
//
// nil parts are skipped for the same reason as in extractText / extractThoughts.
// Parts come from the server, so our own validation cannot rule out nil.
func extractAttachments(resp *genai.GenerateContentResponse, withFileData bool) []Attachment {
	var attachments []Attachment
	for _, part := range candidateParts(firstCandidate(resp)) {
		switch {
		case part == nil:
		case part.InlineData != nil:
			attachments = append(attachments, Attachment{
				MIMEType: part.InlineData.MIMEType,
				Data:     part.InlineData.Data,
			})
		case withFileData && part.FileData != nil && part.FileData.FileURI != "":
			attachments = append(attachments, Attachment{
				MIMEType: part.FileData.MIMEType,
				URI:      part.FileData.FileURI,
			})
		}
	}
	return attachments
}

// tokenUsageFromMetadata converts genai's token usage metadata into the public type.
func tokenUsageFromMetadata(meta *genai.GenerateContentResponseUsageMetadata) *TokenUsage {
	if meta == nil {
		return nil
	}
	return &TokenUsage{
		PromptTokenCount:     meta.PromptTokenCount,
		CandidatesTokenCount: meta.CandidatesTokenCount,
		TotalTokenCount:      meta.TotalTokenCount,
		ThoughtsTokenCount:   meta.ThoughtsTokenCount,
	}
}
