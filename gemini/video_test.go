package gemini

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/genai"
)

func newVideoTestClient(video *fakeVideoClient) *Client {
	return &Client{videoClient: video}
}

// TestStartVideoBuildsImageToVideoRequest checks that the start frame and generation parameters
// are converted correctly into the SDK's input.
func TestStartVideoBuildsImageToVideoRequest(t *testing.T) {
	video := &fakeVideoClient{}
	client := newVideoTestClient(video)
	seed := int64(4242)
	generateAudio := true

	op, err := client.StartVideo(context.Background(), "veo-3.1-generate-001", VideoRequest{
		Prompt:         "slow dolly in",
		Image:          &Attachment{URI: "gs://bucket/kf.png", MIMEType: "image/png"},
		DurationSec:    8,
		Seed:           &seed,
		AspectRatio:    "16:9",
		Resolution:     "1080p",
		NegativePrompt: "text, watermark",
		GenerateAudio:  &generateAudio,
		OutputGCSURI:   "gs://bucket/out/",
		NumberOfVideos: 1,
	})
	if err != nil {
		t.Fatalf("StartVideo() error = %v", err)
	}
	if op.Name != "operations/abc" || op.Done {
		t.Errorf("operation = %+v", op)
	}

	if video.gotModel != "veo-3.1-generate-001" {
		t.Errorf("model = %q", video.gotModel)
	}
	if video.gotSource.Prompt != "slow dolly in" {
		t.Errorf("prompt = %q", video.gotSource.Prompt)
	}
	if video.gotSource.Image == nil || video.gotSource.Image.GCSURI != "gs://bucket/kf.png" {
		t.Errorf("image = %+v", video.gotSource.Image)
	}
	if video.gotConfig.DurationSeconds == nil || *video.gotConfig.DurationSeconds != 8 {
		t.Errorf("durationSeconds = %v", video.gotConfig.DurationSeconds)
	}
	if video.gotConfig.Seed == nil || *video.gotConfig.Seed != 4242 {
		t.Errorf("seed = %v", video.gotConfig.Seed)
	}
	if video.gotConfig.NumberOfVideos != 1 {
		t.Errorf("numberOfVideos = %d, want 1", video.gotConfig.NumberOfVideos)
	}
	if video.gotConfig.OutputGCSURI != "gs://bucket/out/" || video.gotConfig.AspectRatio != "16:9" {
		t.Errorf("config = %+v", video.gotConfig)
	}
	if video.gotConfig.Resolution != "1080p" || video.gotConfig.NegativePrompt != "text, watermark" {
		t.Errorf("config = %+v", video.gotConfig)
	}
	if video.gotConfig.GenerateAudio == nil || !*video.gotConfig.GenerateAudio {
		t.Errorf("generateAudio = %v", video.gotConfig.GenerateAudio)
	}
}

// TestStartVideoBuildsLastFrame checks that the last frame is placed on the config side.
// The SDK keeps them in different places: the start frame in source, the last frame in
// config.
func TestStartVideoBuildsLastFrame(t *testing.T) {
	video := &fakeVideoClient{}
	client := newVideoTestClient(video)

	_, err := client.StartVideo(context.Background(), "veo-3.1-generate-001", VideoRequest{
		Prompt:    "interpolate",
		Image:     &Attachment{URI: "gs://bucket/first.png"},
		LastFrame: &Attachment{URI: "gs://bucket/last.png"},
	})
	if err != nil {
		t.Fatalf("StartVideo() error = %v", err)
	}
	if video.gotConfig.LastFrame == nil || video.gotConfig.LastFrame.GCSURI != "gs://bucket/last.png" {
		t.Errorf("lastFrame = %+v", video.gotConfig.LastFrame)
	}
}

// TestStartVideoBuildsReferenceImages checks that reference images reach the SDK with their type.
func TestStartVideoBuildsReferenceImages(t *testing.T) {
	video := &fakeVideoClient{}
	client := newVideoTestClient(video)

	_, err := client.StartVideo(context.Background(), "veo-3.1-generate-001", VideoRequest{
		Prompt: "a character walking",
		References: []VideoReference{
			{Image: Attachment{URI: "gs://bucket/char.png"}, Type: VideoReferenceAsset},
			{Image: Attachment{}}, // an empty reference is dropped
			{Image: Attachment{URI: "gs://bucket/style.png"}, Type: VideoReferenceStyle},
		},
	})
	if err != nil {
		t.Fatalf("StartVideo() error = %v", err)
	}

	refs := video.gotConfig.ReferenceImages
	if len(refs) != 2 {
		t.Fatalf("referenceImages = %d, want 2 (empty elements are dropped)", len(refs))
	}
	if refs[0].Image.GCSURI != "gs://bucket/char.png" || refs[0].ReferenceType != VideoReferenceAsset {
		t.Errorf("references[0] = %+v", refs[0])
	}
	if refs[1].Image.GCSURI != "gs://bucket/style.png" || refs[1].ReferenceType != VideoReferenceStyle {
		t.Errorf("references[1] = %+v", refs[1])
	}
}

// TestStartVideoBuildsVideoExtension checks that the input video for an extension reaches the SDK.
func TestStartVideoBuildsVideoExtension(t *testing.T) {
	video := &fakeVideoClient{}
	client := newVideoTestClient(video)

	_, err := client.StartVideo(context.Background(), "veo-3.1-generate-001", VideoRequest{
		Prompt: "continue the motion",
		Video:  &Attachment{URI: "gs://bucket/prev.mp4", MIMEType: "video/mp4"},
	})
	if err != nil {
		t.Fatalf("StartVideo() error = %v", err)
	}
	if video.gotSource.Video == nil || video.gotSource.Video.URI != "gs://bucket/prev.mp4" {
		t.Errorf("video = %+v", video.gotSource.Video)
	}
	if video.gotSource.Image != nil {
		t.Errorf("image = %+v, want none", video.gotSource.Image)
	}
}

// TestStartVideoKeepsRetries checks that the submission is sent with the client's retry settings
// intact. Unlike polling, a 429 on submission loses the generation of a whole video.
func TestStartVideoKeepsRetries(t *testing.T) {
	video := &fakeVideoClient{}
	client := newVideoTestClient(video)

	if _, err := client.StartVideo(context.Background(), "veo-test", VideoRequest{Prompt: "a cat"}); err != nil {
		t.Fatalf("StartVideo() error = %v", err)
	}
	if video.gotConfig.HTTPOptions != nil {
		t.Errorf("httpOptions = %+v, want nil (do not cancel out the client settings)", video.gotConfig.HTTPOptions)
	}
}

// TestStartVideoPassesExtraBody checks that fields the SDK has no type for can be sent with
// ExtraBody.
//
// Using something under parameters (a map) as the example is deliberate. ExtraBody merging
// only recurses when both sides are maps, and an array such as instances is replaced wholesale,
// so use ModifyRequestBody to insert into an array.
func TestStartVideoPassesExtraBody(t *testing.T) {
	video := &fakeVideoClient{}
	client := newVideoTestClient(video)

	_, err := client.StartVideo(context.Background(), "veo-3.1-generate-001", VideoRequest{
		Prompt:    "a cat",
		ExtraBody: map[string]any{"parameters": map[string]any{"somePreviewFlag": true}},
	})
	if err != nil {
		t.Fatalf("StartVideo() error = %v", err)
	}

	opts := video.gotConfig.HTTPOptions
	if opts == nil || opts.ExtraBody == nil {
		t.Fatalf("httpOptions = %+v, want ExtraBody passed through", opts)
	}
}

// TestStartVideoPassesModifyRequestBody checks that the hook rewriting the assembled body reaches
// the SDK. It is the only way to add values inside array elements that ExtraBody cannot reach
// (such as Vertex AI's instances[0]).
func TestStartVideoPassesModifyRequestBody(t *testing.T) {
	video := &fakeVideoClient{}
	client := newVideoTestClient(video)

	_, err := client.StartVideo(context.Background(), "veo-3.1-generate-001", VideoRequest{
		Prompt: "a cat",
		ModifyRequestBody: func(body map[string]any) map[string]any {
			body["touched"] = true
			return body
		},
	})
	if err != nil {
		t.Fatalf("StartVideo() error = %v", err)
	}

	opts := video.gotConfig.HTTPOptions
	if opts == nil || opts.ExtrasRequestProvider == nil {
		t.Fatalf("httpOptions = %+v, want ExtrasRequestProvider passed through", opts)
	}
	// The SDK passes the assembled body, so it can be rewritten and returned as is.
	got := opts.ExtrasRequestProvider(map[string]any{"instances": []any{map[string]any{}}})
	if got["touched"] != true {
		t.Errorf("modified body = %+v, want the hook applied", got)
	}
}

// TestStartVideoRejectsInvalidInputCombinations checks that input combinations the API does not
// accept are refused before sending. Veo cannot combine video with image / referenceImages, and
// lastFrame is valid only together with image.
func TestStartVideoRejectsInvalidInputCombinations(t *testing.T) {
	image := &Attachment{URI: "gs://bucket/kf.png"}

	tests := []struct {
		name string
		req  VideoRequest
		want error
	}{
		{
			name: "Image together with Video",
			req:  VideoRequest{Prompt: "p", Image: image, Video: &Attachment{URI: "gs://bucket/prev.mp4"}},
			want: ErrInvalidVideoInput,
		},
		{
			name: "References together with Image",
			req:  VideoRequest{Prompt: "p", Image: image, References: []VideoReference{{Image: Attachment{URI: "gs://bucket/char.png"}}}},
			want: ErrInvalidVideoInput,
		},
		{
			name: "References together with Video",
			req:  VideoRequest{Prompt: "p", Video: &Attachment{URI: "gs://bucket/prev.mp4"}, References: []VideoReference{{Image: Attachment{URI: "gs://bucket/char.png"}}}},
			want: ErrInvalidVideoInput,
		},
		{
			name: "LastFrame without a start frame",
			req:  VideoRequest{Prompt: "p", LastFrame: &Attachment{URI: "gs://bucket/next.png"}},
			want: ErrInvalidVideoInput,
		},
		{
			name: "Data together with URI",
			req:  VideoRequest{Prompt: "p", Image: &Attachment{URI: "gs://bucket/kf.png", Data: []byte{0x89}}},
			want: ErrInvalidVideoInput,
		},
		{
			name: "neither prompt nor media",
			req:  VideoRequest{},
			want: ErrEmptyPrompt,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			video := &fakeVideoClient{}
			client := newVideoTestClient(video)

			_, err := client.StartVideo(context.Background(), "veo-3.1-generate-001", tt.req)
			if !errors.Is(err, tt.want) {
				t.Fatalf("StartVideo() error = %v, want %v", err, tt.want)
			}
			if video.calls != 0 {
				t.Errorf("SDK calls = %d, want refused before sending", video.calls)
			}
		})
	}
}

// TestStartVideoValidatesSeedRange checks that a seed that does not fit in int32 is refused
// before sending (the SDK's seed is int32).
func TestStartVideoValidatesSeedRange(t *testing.T) {
	video := &fakeVideoClient{}
	client := newVideoTestClient(video)
	seed := int64(1) << 40

	_, err := client.StartVideo(context.Background(), "veo-3.1-generate-001", VideoRequest{Prompt: "p", Seed: &seed})
	if !errors.Is(err, ErrInvalidSeed) {
		t.Fatalf("StartVideo() error = %v, want ErrInvalidSeed", err)
	}
}

func TestStartVideoRequiresModelName(t *testing.T) {
	client := newVideoTestClient(&fakeVideoClient{})

	if _, err := client.StartVideo(context.Background(), "", VideoRequest{Prompt: "p"}); !errors.Is(err, ErrEmptyModelName) {
		t.Fatalf("StartVideo() error = %v, want ErrEmptyModelName", err)
	}
}

// TestPollVideoDisablesRetries checks that a single query does not go through the SDK's backoff.
// Polling is itself a repetition mechanism, so waiting further inside it would make the caller's
// interval and timeout meaningless.
func TestPollVideoDisablesRetries(t *testing.T) {
	video := &fakeVideoClient{pollOp: &genai.GenerateVideosOperation{Name: "operations/abc"}}
	client := newVideoTestClient(video)

	if _, err := client.PollVideo(context.Background(), "operations/abc"); err != nil {
		t.Fatalf("PollVideo() error = %v", err)
	}

	cfg := video.gotPollConfig
	if cfg == nil || cfg.HTTPOptions == nil || cfg.HTTPOptions.RetryOptions == nil {
		t.Fatalf("getOperationConfig = %+v, want the retry override applied", cfg)
	}
	if *cfg.HTTPOptions.RetryOptions.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", *cfg.HTTPOptions.RetryOptions.Attempts)
	}
}

// TestPollVideoMapsCompletedOperation checks that the generation results and safety filter
// information of a completed operation are carried over into the public type.
func TestPollVideoMapsCompletedOperation(t *testing.T) {
	video := &fakeVideoClient{
		pollOp: &genai.GenerateVideosOperation{
			Name: "operations/abc",
			Done: true,
			Response: &genai.GenerateVideosResponse{
				GeneratedVideos: []*genai.GeneratedVideo{
					{Video: &genai.Video{URI: "gs://bucket/out.mp4", MIMEType: "video/mp4"}},
					nil,          // missing elements do not cause a failure
					{Video: nil}, // nor do elements without a video
				},
				RAIMediaFilteredCount:   1,
				RAIMediaFilteredReasons: []string{"violence"},
			},
		},
	}
	client := newVideoTestClient(video)

	op, err := client.PollVideo(context.Background(), "operations/abc")
	if err != nil {
		t.Fatalf("PollVideo() error = %v", err)
	}

	if video.gotPollOp == nil || video.gotPollOp.Name != "operations/abc" {
		t.Errorf("polled operation = %+v", video.gotPollOp)
	}
	if !op.Done || len(op.Videos) != 1 || op.Videos[0].URI != "gs://bucket/out.mp4" {
		t.Errorf("operation = %+v", op)
	}
	if op.Videos[0].MIMEType != "video/mp4" {
		t.Errorf("mime type = %q", op.Videos[0].MIMEType)
	}
	if op.FilteredCount != 1 || len(op.FilteredReasons) != 1 {
		t.Errorf("filtered = %d %v", op.FilteredCount, op.FilteredReasons)
	}
	if op.Failure != nil {
		t.Errorf("failure = %v, want none", op.Failure)
	}
}

// TestPollVideoMapsOperationFailure checks that an operation completed as a failure becomes an
// error that can be classified with ErrVideoGenerationFailed.
// Fetching itself succeeded, so PollVideo does not return it as the method's error.
func TestPollVideoMapsOperationFailure(t *testing.T) {
	video := &fakeVideoClient{
		pollOp: &genai.GenerateVideosOperation{
			Name: "operations/abc",
			Done: true,
			Error: map[string]any{
				"code":    float64(3),
				"status":  "INVALID_ARGUMENT",
				"message": "Video duration 36 seconds exceeds the maximum duration 30 seconds",
			},
		},
	}
	client := newVideoTestClient(video)

	op, err := client.PollVideo(context.Background(), "operations/abc")
	if err != nil {
		t.Fatalf("PollVideo() error = %v, want the failure carried on the operation", err)
	}
	if !errors.Is(op.Failure, ErrVideoGenerationFailed) {
		t.Fatalf("failure = %v, want ErrVideoGenerationFailed", op.Failure)
	}
	for _, want := range []string{"code=3", "INVALID_ARGUMENT", "exceeds the maximum duration"} {
		if !strings.Contains(op.Failure.Error(), want) {
			t.Errorf("failure = %q, want it to contain %q", op.Failure.Error(), want)
		}
	}
}

// TestVideoOperationFailureFallsBackToRawPayload checks that no information is lost even for a
// failure with none of the known keys. As long as genai exposes this field as a raw map, its key
// layout is not guaranteed.
func TestVideoOperationFailureFallsBackToRawPayload(t *testing.T) {
	err := videoOperationFailure(map[string]any{"unexpected": "shape"})

	if !errors.Is(err, ErrVideoGenerationFailed) {
		t.Fatalf("failure = %v, want ErrVideoGenerationFailed", err)
	}
	if !strings.Contains(err.Error(), "unexpected") {
		t.Errorf("failure = %q, want it to contain the raw payload", err)
	}

	if got := videoOperationFailure(nil); got != nil {
		t.Errorf("videoOperationFailure(nil) = %v, want nil", got)
	}
}

func TestPollVideoRequiresOperationName(t *testing.T) {
	client := newVideoTestClient(&fakeVideoClient{})

	if _, err := client.PollVideo(context.Background(), "  "); !errors.Is(err, ErrEmptyOperationName) {
		t.Fatalf("PollVideo() error = %v, want ErrEmptyOperationName", err)
	}
}

// TestVideoOperationFromNil checks that a nil operation returned by the SDK is classified as an
// empty response.
func TestVideoOperationFromNil(t *testing.T) {
	_, err := videoOperationFrom(nil)

	if !errors.Is(err, ErrEmptyResponse) {
		t.Errorf("videoOperationFrom(nil) error = %v, want ErrEmptyResponse", err)
	}
}
