package gemini

import (
	"context"
	"time"

	"google.golang.org/genai"
)

// SDK との境界（modelClient / videoClient）に差し込むテストダブルです。
//
// この 2 つの seam があるおかげで、GCP 認証も HTTP も無しにこのパッケージの
// 変換・検証・抽出を丸ごと動かせます。フェイクを 1 か所へ集めているのは、
// 同じ形のスタブがテストごとに少しずつ違う形で増えるのを防ぐためです。
var (
	_ modelClient = (*fakeModelClient)(nil)
	_ modelClient = (*slowModelClient)(nil)
	_ videoClient = (*fakeVideoClient)(nil)
)

// fakeModelClient は生成呼び出しのテストダブルで、SDK へ渡された引数を記録します。
type fakeModelClient struct {
	calls       int
	gotModel    string
	gotConfig   *genai.GenerateContentConfig
	gotContents []*genai.Content

	// resp が nil なら、テキスト "ok" を返す既定のレスポンスを組み立てます。
	resp *genai.GenerateContentResponse
	err  error
}

func (f *fakeModelClient) GenerateContent(_ context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	f.calls++
	f.gotModel = model
	f.gotContents = contents
	f.gotConfig = config

	if f.err != nil {
		return nil, f.err
	}
	if f.resp != nil {
		return f.resp, nil
	}
	return respWithParts(genai.FinishReasonStop, &genai.Part{Text: "ok"}), nil
}

// slowModelClient は RequestTimeout の検証用に、context の期限まで応答しないフェイクです。
type slowModelClient struct {
	delay time.Duration
}

func (s *slowModelClient) GenerateContent(ctx context.Context, _ string, _ []*genai.Content, _ *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(s.delay):
		return respWithParts(genai.FinishReasonStop, &genai.Part{Text: "late"}), nil
	}
}

// fakeVideoClient は動画生成のテストダブルで、SDK へ渡された引数を記録します。
type fakeVideoClient struct {
	calls         int
	gotModel      string
	gotSource     *genai.GenerateVideosSource
	gotConfig     *genai.GenerateVideosConfig
	gotPollOp     *genai.GenerateVideosOperation
	gotPollConfig *genai.GetOperationConfig

	// startOp が nil なら、未完了のオペレーション "operations/abc" を返します。
	startOp  *genai.GenerateVideosOperation
	startErr error
	pollOp   *genai.GenerateVideosOperation
	pollErr  error
}

func (f *fakeVideoClient) GenerateVideosFromSource(_ context.Context, model string, source *genai.GenerateVideosSource, config *genai.GenerateVideosConfig) (*genai.GenerateVideosOperation, error) {
	f.calls++
	f.gotModel, f.gotSource, f.gotConfig = model, source, config

	if f.startErr != nil {
		return nil, f.startErr
	}
	if f.startOp != nil {
		return f.startOp, nil
	}
	return &genai.GenerateVideosOperation{Name: "operations/abc"}, nil
}

func (f *fakeVideoClient) GetVideosOperation(_ context.Context, operation *genai.GenerateVideosOperation, config *genai.GetOperationConfig) (*genai.GenerateVideosOperation, error) {
	f.calls++
	f.gotPollOp, f.gotPollConfig = operation, config
	return f.pollOp, f.pollErr
}

// respWithParts は、1 候補だけを持つ生成レスポンスを組み立てます。
func respWithParts(reason genai.FinishReason, parts ...*genai.Part) *genai.GenerateContentResponse {
	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{FinishReason: reason, Content: &genai.Content{Parts: parts}},
		},
	}
}
