package gemini

import (
	"errors"
	"strings"
	"testing"
)

// TestAttachmentPartsBuildsTextThenInlineData は、プロンプトが先頭に来て添付が
// 順序どおり続くことを検証します。
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
		t.Errorf("parts[0].Text = %q, want プロンプトが先頭", parts[0].Text)
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

// TestAttachmentPartsSkipsEmptyAttachments は、空の添付が落ちることを検証します。
// 呼び出し側は画像を「あれば渡す」形で組み立てるため、空要素の除去を毎回書かせません。
func TestAttachmentPartsSkipsEmptyAttachments(t *testing.T) {
	parts, err := attachmentParts("prompt", []Attachment{
		{MIMEType: "image/png"},
		{MIMEType: "image/png", Data: []byte("real")},
	})
	if err != nil {
		t.Fatalf("attachmentParts() error = %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2（空の添付は落ちる）", len(parts))
	}
}

// TestAttachmentPartsAllowsAttachmentOnlyRequests は、プロンプト無しでも添付だけで
// 送信できることを検証します。音声や画像だけを渡して解析させる用途があります。
func TestAttachmentPartsAllowsAttachmentOnlyRequests(t *testing.T) {
	parts, err := attachmentParts("", []Attachment{{MIMEType: "audio/mpeg", Data: []byte("song")}})
	if err != nil {
		t.Fatalf("attachmentParts() error = %v", err)
	}
	if len(parts) != 1 || parts[0].InlineData == nil {
		t.Fatalf("parts = %+v, want インラインデータ 1 件だけ", parts)
	}
}

// TestAttachmentPartsRejectsEmptyRequests は、送るものが何も無い場合をエラーにする
// ことを検証します。空のリクエストを送ると API 側の分かりにくいエラーになります。
func TestAttachmentPartsRejectsEmptyRequests(t *testing.T) {
	tests := map[string][]Attachment{
		"プロンプトも添付も無い": nil,
		"空の添付だけ":      {{MIMEType: "image/png"}},
	}

	for name, attachments := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := attachmentParts("", attachments); !errors.Is(err, ErrEmptyParts) {
				t.Errorf("attachmentParts() error = %v, want ErrEmptyParts", err)
			}
		})
	}
}

// TestAttachmentPartsRequiresMIMETypeForInlineData は、バイト列に MIME type が
// 無い場合にエラーになることを検証します。API は推測できません。
func TestAttachmentPartsRequiresMIMETypeForInlineData(t *testing.T) {
	_, err := attachmentParts("prompt", []Attachment{{Data: []byte("bytes")}})

	if !errors.Is(err, ErrInvalidAttachment) {
		t.Fatalf("attachmentParts() error = %v, want ErrInvalidAttachment", err)
	}
	if errors.Is(err, ErrEmptyParts) {
		t.Error("MIME type の欠落が ErrEmptyParts に分類されています")
	}
}

// TestAttachmentPartsBuildsFileDataForURIs は、URI 参照がインラインではなく FileData の
// パートになることを検証します。Vertex AI は gs:// をそのまま読めるため、参照で渡す
// 経路をバイト列と同じ型で表せる必要があります。
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
		t.Errorf("parts[1] = %+v, want gs:// 参照", parts[1])
	}
	if parts[1].FileData.MIMEType != "image/png" {
		t.Errorf("parts[1] の MIME type = %q, want そのまま渡ること", parts[1].FileData.MIMEType)
	}
	// MIME type を推測できない URI は、サーバー側の判定に委ねられるよう空のまま送る。
	if parts[2].FileData == nil || parts[2].FileData.MIMEType != "" {
		t.Errorf("parts[2] = %+v, want MIME type は空のまま", parts[2])
	}
	if parts[1].InlineData != nil || parts[2].InlineData != nil {
		t.Error("URI 参照がインラインデータとして送られています")
	}
}

// TestAttachmentValidateSourceClassifiesByCallerSentinel は、同じ「Data と URI の併用」が
// 呼び出し経路ごとに違うセンチネルへ分類されることを検証します。
//
// 定義は 1 か所（validateSource）ですが、生成の入力なら ErrInvalidAttachment、
// 動画生成の入力なら ErrInvalidVideoInput として下流の errors.Is が働く必要があります。
func TestAttachmentValidateSourceClassifiesByCallerSentinel(t *testing.T) {
	both := Attachment{MIMEType: "image/png", Data: []byte("bytes"), URI: "gs://bucket/cover.png"}

	tests := []struct {
		name     string
		sentinel error
		other    error
	}{
		{"生成の入力", ErrInvalidAttachment, ErrInvalidVideoInput},
		{"動画生成の入力", ErrInvalidVideoInput, ErrInvalidAttachment},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := both.validateSource(tt.sentinel, "Image")
			if !errors.Is(err, tt.sentinel) {
				t.Fatalf("validateSource() error = %v, want %v", err, tt.sentinel)
			}
			if errors.Is(err, tt.other) {
				t.Errorf("validateSource() error = %v, want %v に分類されないこと", err, tt.other)
			}
			if !strings.Contains(err.Error(), "Image") {
				t.Errorf("error = %q, want どの入力が不正かを含むこと", err)
			}
		})
	}

	t.Run("片方だけならエラーにしない", func(t *testing.T) {
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

// TestAttachmentPartsRejectsDataAndURITogether は、併用が送信前に弾かれることを
// 検証します。両方送るとどちらが効くかは API 任せになります。
func TestAttachmentPartsRejectsDataAndURITogether(t *testing.T) {
	_, err := attachmentParts("prompt", []Attachment{
		{MIMEType: "image/png", Data: []byte("bytes"), URI: "gs://bucket/cover.png"},
	})

	if !errors.Is(err, ErrInvalidAttachment) {
		t.Fatalf("attachmentParts() error = %v, want ErrInvalidAttachment", err)
	}
}

// TestAttachmentIsEmptyCoversBothCarriers は、URI だけの添付を「空」と見なさないことを
// 検証します。空扱いすると参照ベースの画像が黙って全部落ちます。
func TestAttachmentIsEmptyCoversBothCarriers(t *testing.T) {
	tests := map[string]struct {
		attachment Attachment
		want       bool
	}{
		"何も無い":          {attachment: Attachment{MIMEType: "image/png"}, want: true},
		"Data あり":       {attachment: Attachment{Data: []byte("x")}, want: false},
		"URI あり":        {attachment: Attachment{URI: "gs://bucket/a.png"}, want: false},
		"MIME type も無い": {attachment: Attachment{}, want: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.attachment.IsEmpty(); got != tt.want {
				t.Errorf("IsEmpty() = %v, want %v", got, tt.want)
			}
		})
	}
}
