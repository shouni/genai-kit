package gemini

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/genai"
)

func TestExtractText(t *testing.T) {
	t.Run("concatenates multiple text parts", func(t *testing.T) {
		// Models sometimes split the body across several parts, so concatenation is needed.
		resp := respWithParts(genai.FinishReasonStop,
			&genai.Part{Text: "こんにちは"},
			&genai.Part{Text: "世界"},
		)

		got, err := extractText(resp)
		if err != nil {
			t.Fatalf("extractText() error = %v", err)
		}
		if got != "こんにちは世界" {
			t.Errorf("text is missing: got %q", got)
		}
	})

	t.Run("thought parts do not leak into the body", func(t *testing.T) {
		// Models with thinking enabled return the thought summary before the body.
		// An implementation returning the first non-empty text would return the thought summary instead of the body.
		resp := respWithParts(genai.FinishReasonStop,
			&genai.Part{Text: "まず前提を整理する…", Thought: true},
			&genai.Part{Text: "答えは42です"},
		)

		got, err := extractText(resp)
		if err != nil {
			t.Fatalf("extractText() error = %v", err)
		}
		if got != "答えは42です" {
			t.Errorf("the thought summary leaked into the body: got %q", got)
		}
	})

	t.Run("skips nil parts", func(t *testing.T) {
		resp := respWithParts(genai.FinishReasonStop, nil, &genai.Part{Text: "ok"})

		got, err := extractText(resp)
		if err != nil {
			t.Fatalf("extractText() error = %v", err)
		}
		if got != "ok" {
			t.Errorf("got %q, want %q", got, "ok")
		}
	})

	t.Run("an empty response becomes ErrEmptyResponse", func(t *testing.T) {
		_, err := extractText(&genai.GenerateContentResponse{})

		if !errors.Is(err, ErrEmptyResponse) {
			t.Errorf("extractText() error = %v, want ErrEmptyResponse", err)
		}
		if errors.Is(err, ErrBlocked) {
			t.Error("an empty response is classified as ErrBlocked")
		}
	})

	// When the input itself is refused, there are zero candidates with only PromptFeedback attached.
	// Without looking at it, the failure is reported as an "empty response" with no reason, and it is not conveyed that retrying is pointless.
	t.Run("an input block becomes ErrBlocked with a reason", func(t *testing.T) {
		resp := &genai.GenerateContentResponse{
			PromptFeedback: &genai.GenerateContentResponsePromptFeedback{
				BlockReason:        genai.BlockedReasonProhibitedContent,
				BlockReasonMessage: "policy",
			},
		}
		_, err := extractText(resp)
		if !errors.Is(err, ErrBlocked) {
			t.Fatalf("extractText() error = %v, want ErrBlocked", err)
		}
		if errors.Is(err, ErrEmptyResponse) {
			t.Errorf("a prompt block must not classify as ErrEmptyResponse: %v", err)
		}
		apiErr, ok := errors.AsType[*APIResponseError](err)
		if !ok || apiErr.BlockReason != genai.BlockedReasonProhibitedContent {
			t.Errorf("BlockReason not carried: %+v", err)
		}
		if !strings.Contains(err.Error(), "policy") {
			t.Errorf("BlockReasonMessage not carried: %v", err)
		}
	})

	// Since genai v1.75.0, when a hop in the middle of automatic continuation is refused, the SDK
	// clears CONTINUATION from the earlier hop's candidate and merges keeping only the PromptFeedback.
	// Even with a candidate present, not looking at PromptFeedback would return the partial body as a success.
	t.Run("a block mid-continuation returns no partial text and becomes ErrBlocked", func(t *testing.T) {
		resp := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{{
				Content: &genai.Content{Parts: []*genai.Part{{Text: "途中までの本文"}}},
			}},
			PromptFeedback: &genai.GenerateContentResponsePromptFeedback{
				BlockReason:        genai.BlockedReasonOther,
				BlockReasonMessage: "Blocked on continuation hop",
			},
		}
		text, err := extractText(resp)
		if !errors.Is(err, ErrBlocked) {
			t.Fatalf("extractText() error = %v, want ErrBlocked", err)
		}
		if text != "" {
			t.Errorf("extractText() text = %q, want empty", text)
		}
		apiErr, ok := errors.AsType[*APIResponseError](err)
		if !ok || apiErr.BlockReason != genai.BlockedReasonOther {
			t.Errorf("BlockReason not carried: %+v", err)
		}
	})

	t.Run("PromptFeedback without a reason stays an empty response", func(t *testing.T) {
		resp := &genai.GenerateContentResponse{PromptFeedback: &genai.GenerateContentResponsePromptFeedback{}}
		if _, err := extractText(resp); !errors.Is(err, ErrEmptyResponse) {
			t.Errorf("extractText() error = %v, want ErrEmptyResponse", err)
		}
	})

	t.Run("a nil candidate slot becomes ErrEmptyResponse", func(t *testing.T) {
		// The candidate slot itself is a value from the server and can be nil.
		_, err := extractText(&genai.GenerateContentResponse{Candidates: []*genai.Candidate{nil}})

		if !errors.Is(err, ErrEmptyResponse) {
			t.Errorf("extractText() error = %v, want ErrEmptyResponse", err)
		}
	})

	t.Run("a block carries ErrBlocked and the FinishReason", func(t *testing.T) {
		resp := respWithParts(genai.FinishReasonSafety, &genai.Part{Text: "..."})

		_, err := extractText(resp)

		if !errors.Is(err, ErrBlocked) {
			t.Fatalf("extractText() error = %v, want ErrBlocked", err)
		}
		apiErr, ok := errors.AsType[*APIResponseError](err)
		if !ok {
			t.Fatalf("error type = %T, want *APIResponseError", err)
		}
		if apiErr.FinishReason != genai.FinishReasonSafety {
			t.Errorf("FinishReason = %v, want %v", apiErr.FinishReason, genai.FinishReasonSafety)
		}
	})
}

// TestFinishReasonHasTwoUnsetValues checks that a response with no finish reason is not treated
// as blocked.
//
// genai.FinishReason's zero value is "", a different value from the SDK constant
// FinishReasonUnspecified ("FINISH_REASON_UNSPECIFIED"). The server sometimes returns a response
// with no finish reason, and getting this wrong makes a normal response fail as
// "blocked".
func TestFinishReasonHasTwoUnsetValues(t *testing.T) {
	tests := []struct {
		name        string
		reason      genai.FinishReason
		wantBlocked bool
		wantErr     error
	}{
		{"zero value", "", false, nil},
		{"FINISH_REASON_UNSPECIFIED", genai.FinishReasonUnspecified, false, nil},
		{"STOP", genai.FinishReasonStop, false, nil},
		// MAX_TOKENS is an abnormal end, but the remedy differs, so it is returned with a sentinel other than blocked.
		{"MAX_TOKENS", genai.FinishReasonMaxTokens, true, ErrTruncated},
		// CONTINUATION is a partial result. It arrives when automatic continuation is off, and the body is incomplete just as with MAX_TOKENS.
		{"CONTINUATION", genai.FinishReasonContinuation, true, ErrTruncated},
		{"SAFETY", genai.FinishReasonSafety, true, ErrBlocked},
		{"RECITATION", genai.FinishReasonRecitation, true, ErrBlocked},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBlockedFinishReason(tt.reason); got != tt.wantBlocked {
				t.Errorf("isBlockedFinishReason(%q) = %v, want %v", tt.reason, got, tt.wantBlocked)
			}

			_, err := extractText(respWithParts(tt.reason, &genai.Part{Text: "本文"}))
			if tt.wantBlocked {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("extractText() error = %v, want %v", err, tt.wantErr)
				}
				if tt.wantErr == ErrTruncated && errors.Is(err, ErrBlocked) {
					t.Errorf("%s must not also classify as ErrBlocked: %v", tt.reason, err)
				}
				return
			}
			if err != nil {
				t.Errorf("extractText() error = %v, want nil", err)
			}
		})
	}
}

func TestExtractThoughts(t *testing.T) {
	t.Run("concatenates only thought parts", func(t *testing.T) {
		resp := respWithParts(genai.FinishReasonStop,
			&genai.Part{Text: "思考A", Thought: true},
			&genai.Part{Text: "本文"},
			&genai.Part{Text: "思考B", Thought: true},
		)

		if got := extractThoughts(resp); got != "思考A思考B" {
			t.Errorf("extractThoughts() = %q, want %q", got, "思考A思考B")
		}
	})

	t.Run("empty string when there are no thoughts", func(t *testing.T) {
		resp := respWithParts(genai.FinishReasonStop, &genai.Part{Text: "本文"})

		if got := extractThoughts(resp); got != "" {
			t.Errorf("extractThoughts() = %q, want empty", got)
		}
	})

	t.Run("does not panic on a nil response", func(t *testing.T) {
		if got := extractThoughts(nil); got != "" {
			t.Errorf("extractThoughts(nil) = %q, want empty", got)
		}
	})
}

// TestResponseAttachmentsCarryMIMETypes checks that returned inline data can be extracted with its
// MIME type. Images / Audios are bytes only, so without this the extension or Content-Type for
// saving could not be decided.
func TestResponseAttachmentsCarryMIMETypes(t *testing.T) {
	resp, err := responseFromGenAI(respWithParts(genai.FinishReasonStop,
		&genai.Part{InlineData: &genai.Blob{MIMEType: "image/png", Data: []byte("png")}},
		&genai.Part{InlineData: &genai.Blob{MIMEType: "audio/mpeg", Data: []byte("mp3")}},
	), false)
	if err != nil {
		t.Fatalf("responseFromGenAI() error = %v", err)
	}

	if len(resp.Attachments) != 2 {
		t.Fatalf("Attachments = %d, want 2", len(resp.Attachments))
	}
	if resp.Attachments[0].MIMEType != "image/png" || string(resp.Attachments[0].Data) != "png" {
		t.Errorf("Attachments[0] = %+v", resp.Attachments[0])
	}
	if resp.Attachments[1].MIMEType != "audio/mpeg" {
		t.Errorf("Attachments[1].MIMEType = %q", resp.Attachments[1].MIMEType)
	}
	// Images / Audios are sorted out as subsets of Attachments.
	if len(resp.Images) != 1 || len(resp.Audios) != 1 {
		t.Errorf("Images = %d, Audios = %d, want one each", len(resp.Images), len(resp.Audios))
	}
}

// TestResponseAttachmentsCarryOutputURIs checks that media written to GCS because OutputURIPrefix
// was specified stays in Attachments as FileData URIs. Looking only at InlineData would make the
// output vanish entirely without an error.
func TestResponseAttachmentsCarryOutputURIs(t *testing.T) {
	resp, err := responseFromGenAI(respWithParts(genai.FinishReasonStop,
		&genai.Part{FileData: &genai.FileData{FileURI: "gs://bucket/out/1.png", MIMEType: "image/png"}},
		&genai.Part{FileData: &genai.FileData{MIMEType: "image/png"}}, // FileData without a URI is dropped
		&genai.Part{InlineData: &genai.Blob{MIMEType: "image/png", Data: []byte("png")}},
	), true)
	if err != nil {
		t.Fatalf("responseFromGenAI() error = %v", err)
	}

	if len(resp.Attachments) != 2 {
		t.Fatalf("Attachments = %+v, want 2 entries", resp.Attachments)
	}
	got := resp.Attachments[0]
	if got.URI != "gs://bucket/out/1.png" || got.MIMEType != "image/png" || len(got.Data) != 0 {
		t.Errorf("Attachments[0] = %+v, want only the URI and MIME type", got)
	}
	// Images collects only elements that carry bytes. URI elements do not add empty bytes.
	if len(resp.Images) != 1 || string(resp.Images[0]) != "png" {
		t.Errorf("Images = %q, want only the one inline entry", resp.Images)
	}
}

// TestResponseIgnoresFileDataWithoutOutputURIPrefix checks that FileData is not picked up for
// requests that did not send OutputURIPrefix. Picking it up would let a response that used to be
// a no-data error pass through imagegen / lyria as an element with an empty Data.
func TestResponseIgnoresFileDataWithoutOutputURIPrefix(t *testing.T) {
	resp, err := responseFromGenAI(respWithParts(genai.FinishReasonStop,
		&genai.Part{FileData: &genai.FileData{FileURI: "gs://bucket/out/1.png", MIMEType: "image/png"}},
	), false)
	if err != nil {
		t.Fatalf("responseFromGenAI() error = %v", err)
	}
	if len(resp.Attachments) != 0 {
		t.Errorf("Attachments = %+v, want 0 entries", resp.Attachments)
	}
}

// TestResponseFromGenAISkipsNilParts checks that nil parts in the response do not cause a panic.
// Parts come from the server, so our own input validation cannot rule them out.
func TestResponseFromGenAISkipsNilParts(t *testing.T) {
	got, err := responseFromGenAI(respWithParts(genai.FinishReasonStop,
		nil,
		&genai.Part{Text: "解説"},
		&genai.Part{InlineData: &genai.Blob{MIMEType: "image/png", Data: []byte("img")}},
		nil,
		&genai.Part{InlineData: &genai.Blob{MIMEType: "audio/mpeg", Data: []byte("snd")}},
	), false)
	if err != nil {
		t.Fatalf("responseFromGenAI() error = %v", err)
	}

	if got.Text != "解説" {
		t.Errorf("Text = %q, want %q", got.Text, "解説")
	}
	if len(got.Attachments) != 2 {
		t.Fatalf("Attachments = %d, want 2", len(got.Attachments))
	}
	if len(got.Images) != 1 || string(got.Images[0]) != "img" {
		t.Errorf("Images = %q", got.Images)
	}
	if len(got.Audios) != 1 || string(got.Audios[0]) != "snd" {
		t.Errorf("Audios = %q", got.Audios)
	}
}

// TestResponseFromGenAIIgnoresUnknownMIMETypes checks that inline data that is neither image nor
// audio stays in Attachments and is not sorted into Images / Audios.
func TestResponseFromGenAIIgnoresUnknownMIMETypes(t *testing.T) {
	got, err := responseFromGenAI(respWithParts(genai.FinishReasonStop,
		&genai.Part{InlineData: &genai.Blob{MIMEType: "application/pdf", Data: []byte("pdf")}},
	), false)
	if err != nil {
		t.Fatalf("responseFromGenAI() error = %v", err)
	}

	if len(got.Attachments) != 1 || got.Attachments[0].MIMEType != "application/pdf" {
		t.Errorf("Attachments = %+v, want the PDF kept", got.Attachments)
	}
	if len(got.Images) != 0 || len(got.Audios) != 0 {
		t.Errorf("Images = %d, Audios = %d, want both 0", len(got.Images), len(got.Audios))
	}
}

func TestTokenUsageFromMetadata(t *testing.T) {
	t.Run("nil when there is no metadata", func(t *testing.T) {
		if got := tokenUsageFromMetadata(nil); got != nil {
			t.Errorf("tokenUsageFromMetadata(nil) = %+v, want nil", got)
		}
	})

	t.Run("copies including thought tokens", func(t *testing.T) {
		got := tokenUsageFromMetadata(&genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     10,
			CandidatesTokenCount: 5,
			TotalTokenCount:      20,
			ThoughtsTokenCount:   5,
		})

		want := TokenUsage{PromptTokenCount: 10, CandidatesTokenCount: 5, TotalTokenCount: 20, ThoughtsTokenCount: 5}
		if got == nil || *got != want {
			t.Errorf("tokenUsageFromMetadata() = %+v, want %+v", got, want)
		}
	})
}
