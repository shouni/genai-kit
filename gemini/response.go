package gemini

import (
	"strings"

	"google.golang.org/genai"
)

// Response は生成結果のラッパーです。
type Response struct {
	Text   string
	Images [][]byte // 生成画像 (InlineData) を保持します
	Audios [][]byte // Lyria 3 等の音声データ
	// Attachments は返却されたインラインデータを MIME type 付きで保持します。
	//
	// Images / Audios はバイト列だけなので、保存時の拡張子や Content-Type を決める
	// 必要がある呼び出し側のために、型を保ったまま取り出せる形を用意しています。
	// 順序は API の返却順です。
	Attachments []Attachment
	// Thoughts は思考サマリです。GenerateOptions.IncludeThoughts が true で、
	// かつモデルが思考サマリを返した場合にのみ設定されます。
	// Text には含まれません。
	Thoughts string
	Usage    *TokenUsage
}

// TokenUsage は生成レスポンスのトークン使用量です。
type TokenUsage struct {
	PromptTokenCount     int32
	CandidatesTokenCount int32
	TotalTokenCount      int32
	// ThoughtsTokenCount は思考に消費されたトークン数です。
	// 課金対象になるため、思考機能を使う場合はこの値を監視してください。
	ThoughtsTokenCount int32
}

// responseFromGenAI は genai のレスポンスをパッケージ公開型の Response に変換します。
func responseFromGenAI(resp *genai.GenerateContentResponse) (*Response, error) {
	text, err := extractText(resp)
	if err != nil {
		return nil, err
	}

	// MIME type で振り分ける。Images / Audios は Attachments の部分集合で、
	// 型を意識せずバイト列だけ欲しい呼び出し側のための入口です。
	attachments := extractInlineData(resp)
	var images [][]byte
	var audios [][]byte
	for _, attachment := range attachments {
		switch {
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

// firstCandidate は、レスポンスの先頭候補を返します。候補が無い場合は nil です。
//
// パートやコンテンツと同様、候補スロット自体もサーバーから来る値であり nil であり得ます。
// テキスト・思考・インラインデータの各抽出関数が個別に nil 判定を書くと、判定の
// 抜け漏れが関数ごとにずれるため（実際にずれていた）、走査の入口をここに集約しています。
func firstCandidate(resp *genai.GenerateContentResponse) *genai.Candidate {
	if resp == nil || len(resp.Candidates) == 0 {
		return nil
	}
	return resp.Candidates[0]
}

// candidateParts は、候補のパート列を返します。候補やコンテンツが nil の場合は nil です。
func candidateParts(candidate *genai.Candidate) []*genai.Part {
	if candidate == nil || candidate.Content == nil {
		return nil
	}
	return candidate.Content.Parts
}

// extractText は resp からテキストを抽出します。
func extractText(resp *genai.GenerateContentResponse) (string, error) {
	candidate := firstCandidate(resp)
	if candidate == nil {
		return "", newEmptyResponseError()
	}

	// FinishReason が正常（未設定 または STOP）以外の場合は、ブロックされたとみなします。
	// 未設定の判定は isUnsetFinishReason に集約しています（ゼロ値と SDK 定数が別値のため）。
	if isBlockedFinishReason(candidate.FinishReason) {
		return "", newBlockedError(candidate.FinishReason)
	}

	// すべてのテキストパートを連結して返します。
	//
	// 思考機能 (ThinkingBudget) が有効なモデルは、思考サマリを Thought=true の
	// テキストパートとして本文より前に返します。最初の非空テキストを返す実装では
	// 本文ではなく思考サマリを返してしまうため、Thought パートは除外します。
	// また、モデルは本文を複数パートに分割して返すことがあるため連結が必要です。
	var sb strings.Builder
	for _, part := range candidateParts(candidate) {
		if part == nil || part.Thought || part.Text == "" {
			continue
		}
		sb.WriteString(part.Text)
	}

	return sb.String(), nil
}

// extractThoughts は思考サマリ（Thought=true のテキストパート）を連結して返します。
// 思考機能が無効な場合や思考サマリが返されない場合は空文字列になります。
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

// extractInlineData は、レスポンスに含まれるインラインデータを MIME type 付きで
// 返却順のまま取り出します。
//
// nil のパートを読み飛ばすのは extractText / extractThoughts と同じ理由です。
// パートはサーバーから来るものなので、こちら側の検証で nil を排除できません。
func extractInlineData(resp *genai.GenerateContentResponse) []Attachment {
	var attachments []Attachment
	for _, part := range candidateParts(firstCandidate(resp)) {
		if part == nil || part.InlineData == nil {
			continue
		}
		attachments = append(attachments, Attachment{
			MIMEType: part.InlineData.MIMEType,
			Data:     part.InlineData.Data,
		})
	}
	return attachments
}

// tokenUsageFromMetadata は genai のトークン使用量メタデータを公開型に変換します。
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
