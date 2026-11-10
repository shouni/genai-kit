package gemini

import (
	"context"
	"time"

	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/operations"
)

// Test doubles plugged into the boundary with the SDK (modelClient / videoClient).
//
// These two seams let this package's conversion, validation and extraction run in full
// with neither GCP credentials nor HTTP. The fakes are gathered in one place to keep
// stubs of the same shape from multiplying in slightly different forms across tests.
var (
	_ modelClient = (*fakeModelClient)(nil)
	_ modelClient = (*slowModelClient)(nil)
	_ videoClient = (*fakeVideoClient)(nil)

	_ interactionClient = (*fakeInteractionClient)(nil)
)

// fakeInteractionClient is a test double for Interactions calls; it records the requests sent.
type fakeInteractionClient struct {
	calls   int
	gotReq  operations.CreateInteractionRequest
	gotOpts []operations.Option

	resp *operations.CreateInteractionResponse
	err  error
}

func (f *fakeInteractionClient) Create(_ context.Context, request operations.CreateInteractionRequest, opts ...operations.Option) (*operations.CreateInteractionResponse, error) {
	f.calls++
	f.gotReq = request
	f.gotOpts = opts
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

// fakeModelClient is a test double for generation calls; it records the arguments passed to the SDK.
type fakeModelClient struct {
	calls       int
	gotModel    string
	gotConfig   *genai.GenerateContentConfig
	gotContents []*genai.Content

	// If resp is nil, a default response returning the text "ok" is built.
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

// slowModelClient is a fake that does not respond until the context deadline, for testing RequestTimeout.
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

// fakeVideoClient is a test double for video generation; it records the arguments passed to the SDK.
type fakeVideoClient struct {
	calls         int
	gotModel      string
	gotSource     *genai.GenerateVideosSource
	gotConfig     *genai.GenerateVideosConfig
	gotPollOp     *genai.GenerateVideosOperation
	gotPollConfig *genai.GetOperationConfig

	// If startOp is nil, an unfinished operation "operations/abc" is returned.
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

// respWithParts builds a generation response with a single candidate.
func respWithParts(reason genai.FinishReason, parts ...*genai.Part) *genai.GenerateContentResponse {
	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{FinishReason: reason, Content: &genai.Content{Parts: parts}},
		},
	}
}
