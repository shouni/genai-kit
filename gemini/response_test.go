package gemini

import (
	"errors"
	"testing"

	"google.golang.org/genai"
)

func TestExtractText(t *testing.T) {
	t.Run("複数のテキストパートが連結されること", func(t *testing.T) {
		// モデルは本文を複数パートに分割して返すことがあるため、連結が必要です。
		resp := respWithParts(genai.FinishReasonStop,
			&genai.Part{Text: "こんにちは"},
			&genai.Part{Text: "世界"},
		)

		got, err := extractText(resp)
		if err != nil {
			t.Fatalf("extractText() error = %v", err)
		}
		if got != "こんにちは世界" {
			t.Errorf("テキストが欠落しています: got %q", got)
		}
	})

	t.Run("思考パートが本文に混入しないこと", func(t *testing.T) {
		// 思考機能が有効なモデルは思考サマリを本文より前に返します。
		// 最初の非空テキストを返す実装では、本文ではなく思考サマリが返ります。
		resp := respWithParts(genai.FinishReasonStop,
			&genai.Part{Text: "まず前提を整理する…", Thought: true},
			&genai.Part{Text: "答えは42です"},
		)

		got, err := extractText(resp)
		if err != nil {
			t.Fatalf("extractText() error = %v", err)
		}
		if got != "答えは42です" {
			t.Errorf("思考サマリが本文に混入しています: got %q", got)
		}
	})

	t.Run("nil パートを飛ばすこと", func(t *testing.T) {
		resp := respWithParts(genai.FinishReasonStop, nil, &genai.Part{Text: "ok"})

		got, err := extractText(resp)
		if err != nil {
			t.Fatalf("extractText() error = %v", err)
		}
		if got != "ok" {
			t.Errorf("got %q, want %q", got, "ok")
		}
	})

	t.Run("空レスポンスは ErrEmptyResponse になること", func(t *testing.T) {
		_, err := extractText(&genai.GenerateContentResponse{})

		if !errors.Is(err, ErrEmptyResponse) {
			t.Errorf("extractText() error = %v, want ErrEmptyResponse", err)
		}
		if errors.Is(err, ErrBlocked) {
			t.Error("空レスポンスが ErrBlocked に分類されています")
		}
	})

	t.Run("nil の候補スロットは ErrEmptyResponse になること", func(t *testing.T) {
		// 候補スロット自体もサーバー由来の値で nil があり得ます。
		_, err := extractText(&genai.GenerateContentResponse{Candidates: []*genai.Candidate{nil}})

		if !errors.Is(err, ErrEmptyResponse) {
			t.Errorf("extractText() error = %v, want ErrEmptyResponse", err)
		}
	})

	t.Run("ブロックは ErrBlocked と FinishReason を持つこと", func(t *testing.T) {
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

// TestFinishReasonHasTwoUnsetValues は、終了理由が未設定のレスポンスをブロック扱い
// しないことを検証します。
//
// genai.FinishReason のゼロ値は "" で、SDK 定数 FinishReasonUnspecified
// ("FINISH_REASON_UNSPECIFIED") とは別値です。サーバーは終了理由を含まない
// レスポンスを返すことがあり、ここを取り違えると正常な応答が
// 「ブロックされました」で失敗します。
func TestFinishReasonHasTwoUnsetValues(t *testing.T) {
	tests := []struct {
		name        string
		reason      genai.FinishReason
		wantBlocked bool
	}{
		{"ゼロ値", "", false},
		{"FINISH_REASON_UNSPECIFIED", genai.FinishReasonUnspecified, false},
		{"STOP", genai.FinishReasonStop, false},
		{"MAX_TOKENS", genai.FinishReasonMaxTokens, true},
		{"SAFETY", genai.FinishReasonSafety, true},
		{"RECITATION", genai.FinishReasonRecitation, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBlockedFinishReason(tt.reason); got != tt.wantBlocked {
				t.Errorf("isBlockedFinishReason(%q) = %v, want %v", tt.reason, got, tt.wantBlocked)
			}

			_, err := extractText(respWithParts(tt.reason, &genai.Part{Text: "本文"}))
			if tt.wantBlocked {
				if !errors.Is(err, ErrBlocked) {
					t.Errorf("extractText() error = %v, want ErrBlocked", err)
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
	t.Run("思考パートのみを連結すること", func(t *testing.T) {
		resp := respWithParts(genai.FinishReasonStop,
			&genai.Part{Text: "思考A", Thought: true},
			&genai.Part{Text: "本文"},
			&genai.Part{Text: "思考B", Thought: true},
		)

		if got := extractThoughts(resp); got != "思考A思考B" {
			t.Errorf("extractThoughts() = %q, want %q", got, "思考A思考B")
		}
	})

	t.Run("思考がなければ空文字列", func(t *testing.T) {
		resp := respWithParts(genai.FinishReasonStop, &genai.Part{Text: "本文"})

		if got := extractThoughts(resp); got != "" {
			t.Errorf("extractThoughts() = %q, want 空", got)
		}
	})

	t.Run("nil レスポンスでも panic しないこと", func(t *testing.T) {
		if got := extractThoughts(nil); got != "" {
			t.Errorf("extractThoughts(nil) = %q, want 空", got)
		}
	})
}

// TestResponseAttachmentsCarryMIMETypes は、返却されたインラインデータが MIME type
// 込みで取り出せることを検証します。Images / Audios はバイト列だけなので、これが
// 無いと保存時の拡張子や Content-Type を決められません。
func TestResponseAttachmentsCarryMIMETypes(t *testing.T) {
	resp, err := responseFromGenAI(respWithParts(genai.FinishReasonStop,
		&genai.Part{InlineData: &genai.Blob{MIMEType: "image/png", Data: []byte("png")}},
		&genai.Part{InlineData: &genai.Blob{MIMEType: "audio/mpeg", Data: []byte("mp3")}},
	))
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
	// Images / Audios は Attachments の部分集合として振り分けられる。
	if len(resp.Images) != 1 || len(resp.Audios) != 1 {
		t.Errorf("Images = %d, Audios = %d, want 各 1 件", len(resp.Images), len(resp.Audios))
	}
}

// TestResponseFromGenAISkipsNilParts は、レスポンス中の nil パートで panic しないことを
// 検証します。パートはサーバー由来なので、こちらの入力検証では排除できません。
func TestResponseFromGenAISkipsNilParts(t *testing.T) {
	got, err := responseFromGenAI(respWithParts(genai.FinishReasonStop,
		nil,
		&genai.Part{Text: "解説"},
		&genai.Part{InlineData: &genai.Blob{MIMEType: "image/png", Data: []byte("img")}},
		nil,
		&genai.Part{InlineData: &genai.Blob{MIMEType: "audio/mpeg", Data: []byte("snd")}},
	))
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

// TestResponseFromGenAIIgnoresUnknownMIMETypes は、画像でも音声でもないインライン
// データが Attachments には残り、Images / Audios には振り分けられないことを
// 検証します。
func TestResponseFromGenAIIgnoresUnknownMIMETypes(t *testing.T) {
	got, err := responseFromGenAI(respWithParts(genai.FinishReasonStop,
		&genai.Part{InlineData: &genai.Blob{MIMEType: "application/pdf", Data: []byte("pdf")}},
	))
	if err != nil {
		t.Fatalf("responseFromGenAI() error = %v", err)
	}

	if len(got.Attachments) != 1 || got.Attachments[0].MIMEType != "application/pdf" {
		t.Errorf("Attachments = %+v, want PDF が残ること", got.Attachments)
	}
	if len(got.Images) != 0 || len(got.Audios) != 0 {
		t.Errorf("Images = %d, Audios = %d, want いずれも 0", len(got.Images), len(got.Audios))
	}
}

func TestTokenUsageFromMetadata(t *testing.T) {
	t.Run("メタデータが無ければ nil", func(t *testing.T) {
		if got := tokenUsageFromMetadata(nil); got != nil {
			t.Errorf("tokenUsageFromMetadata(nil) = %+v, want nil", got)
		}
	})

	t.Run("思考トークンを含めて写すこと", func(t *testing.T) {
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
