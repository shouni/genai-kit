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

// TestStartVideoBuildsImageToVideoRequest は、開始フレームと生成パラメータが SDK の
// 入力へ正しく変換されることを検証します。
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

// TestStartVideoBuildsLastFrame は、終了フレームが設定側（config）へ載ることを
// 検証します。開始フレームは source、終了フレームは config と、SDK 側で置き場所が
// 分かれています。
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

// TestStartVideoBuildsReferenceImages は、参照画像が種別付きで SDK へ渡ることを
// 検証します。
func TestStartVideoBuildsReferenceImages(t *testing.T) {
	video := &fakeVideoClient{}
	client := newVideoTestClient(video)

	_, err := client.StartVideo(context.Background(), "veo-3.1-generate-001", VideoRequest{
		Prompt: "a character walking",
		References: []VideoReference{
			{Image: Attachment{URI: "gs://bucket/char.png"}, Type: VideoReferenceAsset},
			{Image: Attachment{}}, // 空の参照は落とす
			{Image: Attachment{URI: "gs://bucket/style.png"}, Type: VideoReferenceStyle},
		},
	})
	if err != nil {
		t.Fatalf("StartVideo() error = %v", err)
	}

	refs := video.gotConfig.ReferenceImages
	if len(refs) != 2 {
		t.Fatalf("referenceImages = %d, want 2（空の要素は落とす）", len(refs))
	}
	if refs[0].Image.GCSURI != "gs://bucket/char.png" || refs[0].ReferenceType != VideoReferenceAsset {
		t.Errorf("references[0] = %+v", refs[0])
	}
	if refs[1].Image.GCSURI != "gs://bucket/style.png" || refs[1].ReferenceType != VideoReferenceStyle {
		t.Errorf("references[1] = %+v", refs[1])
	}
}

// TestStartVideoBuildsVideoExtension は、継続生成の入力動画が SDK へ渡ることを
// 検証します。
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
		t.Errorf("image = %+v, want なし", video.gotSource.Image)
	}
}

// TestStartVideoKeepsRetries は、投函がクライアントのリトライ設定のまま送られることを
// 検証します。ポーリングと違い、投函での 429 は動画 1 本分の生成を落とします。
func TestStartVideoKeepsRetries(t *testing.T) {
	video := &fakeVideoClient{}
	client := newVideoTestClient(video)

	if _, err := client.StartVideo(context.Background(), "veo-test", VideoRequest{Prompt: "a cat"}); err != nil {
		t.Fatalf("StartVideo() error = %v", err)
	}
	if video.gotConfig.HTTPOptions != nil {
		t.Errorf("httpOptions = %+v, want nil（クライアント設定を打ち消さない）", video.gotConfig.HTTPOptions)
	}
}

// TestStartVideoPassesExtraBody は、SDK が型として持たないフィールドを ExtraBody で
// 送れることを検証します。
//
// ここで parameters 配下（マップ）を例にしているのは意図的です。ExtraBody のマージは
// マップ同士のときだけ再帰し、instances のような配列は丸ごと置き換わってしまうため、
// 配列へ差し込む用途には ModifyRequestBody を使います。
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
		t.Fatalf("httpOptions = %+v, want ExtraBody が渡ること", opts)
	}
}

// TestStartVideoPassesModifyRequestBody は、組み立て済みボディを書き換えるフックが
// SDK へ渡ることを検証します。ExtraBody では届かない配列要素の中（Vertex AI の
// instances[0] など）へ値を足すための唯一の手段です。
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
		t.Fatalf("httpOptions = %+v, want ExtrasRequestProvider が渡ること", opts)
	}
	// SDK は組み立て済みボディを渡してくるので、そのまま書き換えて返せる。
	got := opts.ExtrasRequestProvider(map[string]any{"instances": []any{map[string]any{}}})
	if got["touched"] != true {
		t.Errorf("modified body = %+v, want フックが適用されること", got)
	}
}

// TestStartVideoRejectsInvalidInputCombinations は、API が受け付けない入力の
// 組み合わせを送信前に弾くことを検証します。Veo は video と image / referenceImages を
// 併用できず、lastFrame は image とセットでのみ有効です。
func TestStartVideoRejectsInvalidInputCombinations(t *testing.T) {
	image := &Attachment{URI: "gs://bucket/kf.png"}

	tests := []struct {
		name string
		req  VideoRequest
		want error
	}{
		{
			name: "Image と Video の併用",
			req:  VideoRequest{Prompt: "p", Image: image, Video: &Attachment{URI: "gs://bucket/prev.mp4"}},
			want: ErrInvalidVideoInput,
		},
		{
			name: "References と Image の併用",
			req:  VideoRequest{Prompt: "p", Image: image, References: []VideoReference{{Image: Attachment{URI: "gs://bucket/char.png"}}}},
			want: ErrInvalidVideoInput,
		},
		{
			name: "References と Video の併用",
			req:  VideoRequest{Prompt: "p", Video: &Attachment{URI: "gs://bucket/prev.mp4"}, References: []VideoReference{{Image: Attachment{URI: "gs://bucket/char.png"}}}},
			want: ErrInvalidVideoInput,
		},
		{
			name: "開始フレーム無しの LastFrame",
			req:  VideoRequest{Prompt: "p", LastFrame: &Attachment{URI: "gs://bucket/next.png"}},
			want: ErrInvalidVideoInput,
		},
		{
			name: "Data と URI の併用",
			req:  VideoRequest{Prompt: "p", Image: &Attachment{URI: "gs://bucket/kf.png", Data: []byte{0x89}}},
			want: ErrInvalidVideoInput,
		},
		{
			name: "プロンプトもメディアも無い",
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
				t.Errorf("SDK 呼び出し = %d 回, want 送信前に弾かれること", video.calls)
			}
		})
	}
}

// TestStartVideoValidatesSeedRange は、int32 に収まらないシードを送信前に弾くことを
// 検証します（SDK のシードは int32）。
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

// TestPollVideoDisablesRetries は、1 回の問い合わせが SDK のバックオフを挟まないことを
// 検証します。ポーリング自体が繰り返しの仕組みなので、内側でさらに待つと呼び出し側の
// 間隔とタイムアウトが意味を失います。
func TestPollVideoDisablesRetries(t *testing.T) {
	video := &fakeVideoClient{pollOp: &genai.GenerateVideosOperation{Name: "operations/abc"}}
	client := newVideoTestClient(video)

	if _, err := client.PollVideo(context.Background(), "operations/abc"); err != nil {
		t.Fatalf("PollVideo() error = %v", err)
	}

	cfg := video.gotPollConfig
	if cfg == nil || cfg.HTTPOptions == nil || cfg.HTTPOptions.RetryOptions == nil {
		t.Fatalf("getOperationConfig = %+v, want リトライ打ち消しが載ること", cfg)
	}
	if *cfg.HTTPOptions.RetryOptions.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", *cfg.HTTPOptions.RetryOptions.Attempts)
	}
}

// TestPollVideoMapsCompletedOperation は、完了したオペレーションの生成結果と
// 安全性フィルタの情報が公開型へ移されることを検証します。
func TestPollVideoMapsCompletedOperation(t *testing.T) {
	video := &fakeVideoClient{
		pollOp: &genai.GenerateVideosOperation{
			Name: "operations/abc",
			Done: true,
			Response: &genai.GenerateVideosResponse{
				GeneratedVideos: []*genai.GeneratedVideo{
					{Video: &genai.Video{URI: "gs://bucket/out.mp4", MIMEType: "video/mp4"}},
					nil,          // 欠けた要素があっても落とさない
					{Video: nil}, // 動画の無い要素も同様
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
		t.Errorf("failure = %v, want なし", op.Failure)
	}
}

// TestPollVideoMapsOperationFailure は、失敗として完了したオペレーションが
// ErrVideoGenerationFailed で判定できる error になることを検証します。
// 取得自体は成功しているため、PollVideo はメソッドのエラーとしては返しません。
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
		t.Fatalf("PollVideo() error = %v, want 失敗はオペレーション側に載ること", err)
	}
	if !errors.Is(op.Failure, ErrVideoGenerationFailed) {
		t.Fatalf("failure = %v, want ErrVideoGenerationFailed", op.Failure)
	}
	for _, want := range []string{"code=3", "INVALID_ARGUMENT", "exceeds the maximum duration"} {
		if !strings.Contains(op.Failure.Error(), want) {
			t.Errorf("failure = %q, want %q を含むこと", op.Failure.Error(), want)
		}
	}
}

// TestVideoOperationFailureFallsBackToRawPayload は、既知のキーが 1 つも無い失敗でも
// 情報を落とさないことを検証します。genai がこのフィールドを map のまま公開している
// 以上、キーの構成は保証されません。
func TestVideoOperationFailureFallsBackToRawPayload(t *testing.T) {
	err := videoOperationFailure(map[string]any{"unexpected": "shape"})

	if !errors.Is(err, ErrVideoGenerationFailed) {
		t.Fatalf("failure = %v, want ErrVideoGenerationFailed", err)
	}
	if !strings.Contains(err.Error(), "unexpected") {
		t.Errorf("failure = %q, want 生のペイロードを含むこと", err)
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

// TestVideoOperationFromNil は、SDK が nil のオペレーションを返した場合に
// 空レスポンスとして分類されることを検証します。
func TestVideoOperationFromNil(t *testing.T) {
	_, err := videoOperationFrom(nil)

	if !errors.Is(err, ErrEmptyResponse) {
		t.Errorf("videoOperationFrom(nil) error = %v, want ErrEmptyResponse", err)
	}
}
