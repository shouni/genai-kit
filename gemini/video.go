package gemini

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"
)

// VideoGenerator は、動画生成の長時間実行オペレーションを開始し進捗を確認する、
// 最小のインターフェースです。
//
// 完了までの待ち方（ポーリング間隔・タイムアウト・一時エラーの許容回数）は含めて
// いません。それはこの2つを呼ぶループ側の方針であり、実装を差し替える理由にならない
// ためです。veo パッケージがこのインターフェースを受け取ってループを組みます。
type VideoGenerator interface {
	StartVideo(ctx context.Context, model string, req VideoRequest) (*VideoOperation, error)
	PollVideo(ctx context.Context, operationName string) (*VideoOperation, error)
}

// VideoReferenceType は、referenceImages に渡した画像がどう使われるかです
// （genai.VideoGenerationReferenceType の別名）。
type VideoReferenceType = genai.VideoGenerationReferenceType

// 参照画像の種別です。
const (
	// VideoReferenceAsset は、画像に写っている被写体（キャラクター・小物など）を
	// 動画に登場させるための参照です。最大3枚まで指定できます。
	VideoReferenceAsset VideoReferenceType = genai.VideoGenerationReferenceTypeAsset
	// VideoReferenceStyle は、画像の画風を動画へ反映させるための参照です。
	VideoReferenceStyle VideoReferenceType = genai.VideoGenerationReferenceTypeStyle
)

// VideoReference は referenceImages に渡す参照画像1枚です。
type VideoReference struct {
	// Image は参照する画像です。URI（Vertex AI では gs://）かバイト列を指定します。
	Image Attachment
	// Type は、この画像をどう使うかです。未指定は API のデフォルトに委ねます。
	Type VideoReferenceType
}

// VideoRequest は動画生成1本分の入力です。
//
// 動画の入力は Image / Video / References の3系統が排他で、モデルが解釈できる
// 組み合わせは限られます（Veo は video と image / referenceImages を併用できません）。
// どの組み合わせを選ぶかは呼び出し側の判断ですが、SDK が確実に拒否する組み合わせは
// StartVideo が送信前に ErrInvalidVideoInput で弾きます。
type VideoRequest struct {
	// Prompt は生成指示です。Image / Video のいずれも無い場合は必須で、
	// References を使う場合も API 側が必須としています。
	Prompt string
	// Image は動画の開始フレームです（image-to-video）。
	Image *Attachment
	// Video は継続生成の入力動画です（video extension）。Image とは排他です。
	Video *Attachment
	// LastFrame は動画の終了フレームです（first/last frame 補間）。
	// API 上 Image とセットでのみ有効なため、Image なしでは指定できません。
	LastFrame *Attachment
	// References は被写体や画風の参照画像です。Image / Video / LastFrame とは排他です。
	References []VideoReference

	// DurationSec は生成する動画の秒数です。0 は API のデフォルトに委ねます。
	// 受け付けられる値はモデルと入力の組み合わせによって異なります。
	DurationSec int
	// Seed は生成の再現性を確保するための乱数シードです。nil は毎回ランダムです。
	// int32 の範囲を超える値は ErrInvalidSeed になります。
	Seed *int64
	// AspectRatio は "16:9" / "9:16" などの縦横比です。
	AspectRatio string
	// Resolution は "720p" / "1080p" などの解像度です。
	Resolution string
	// NegativePrompt は生成に含めたくない要素の指定です。
	NegativePrompt string
	// GenerateAudio は音声を同時生成するかです。nil は API のデフォルトに委ねます。
	GenerateAudio *bool
	// OutputGCSURI は生成結果の保存先 GCS バケットです。指定しない場合、結果は
	// バイト列として返ります（長尺動画では応答が大きくなるため通常は指定します）。
	OutputGCSURI string
	// NumberOfVideos は生成する本数です。0 は API のデフォルト（通常1本）です。
	NumberOfVideos int

	// ExtraBody はリクエストボディへ追加で差し込むフィールドです。
	//
	// SDK がまだ型として持っていないプレビュー機能を使うための逃げ道で、構造は
	// バックエンドの REST API に一致させる必要があります。SDK が対応している項目は
	// 上のフィールドを使ってください（ExtraBody は型検査も検証も効きません）。
	//
	// マージはマップ同士のときだけ再帰します。同じキーの値が配列の場合は再帰せず
	// 丸ごと置き換わるため、Vertex AI のリクエストで instances のような配列へ値を
	// 足す用途には使えません（prompt や画像入力ごと消えます）。配列の要素へ差し込む
	// 場合は ModifyRequestBody を使ってください。
	ExtraBody map[string]any

	// ModifyRequestBody は、組み立て済みのリクエストボディを送信直前に書き換えます。
	// ExtraBody のマージが済んだ後に、ボディ全体を受け取って呼ばれます。
	//
	// ExtraBody では届かない位置（配列の要素の中など）へ値を差し込むための手段です。
	// ボディの構造はバックエンドの REST API そのもので、型検査も検証も効きません。
	// 受け取ったマップを直接書き換えて返して構いません。
	ModifyRequestBody func(body map[string]any) map[string]any
}

// VideoOperation は動画生成の長時間実行オペレーションの状態です。
//
// 生成が完了しているかは Done で判定します。Done かつ Failure が非 nil の場合は
// 生成そのものが失敗しています（オペレーションの取得自体は成功しているため、
// StartVideo / PollVideo はエラーを返さずこのフィールドに載せます）。
type VideoOperation struct {
	// Name はオペレーションの識別子です。PollVideo に渡して進捗を確認します。
	Name string
	// Done は生成が完了（成功・失敗を問わず）したかです。
	Done bool
	// Videos は生成された動画です。OutputGCSURI を指定した場合は URI が、
	// 指定しなかった場合は Data が入ります。
	Videos []Attachment
	// FilteredCount は安全性ポリシーで除外された本数です。
	FilteredCount int32
	// FilteredReasons は除外された理由です。
	FilteredReasons []string
	// Failure は生成が失敗した場合の理由です。成功時は nil です。
	Failure error
}

// StartVideo は動画生成の長時間実行オペレーションを開始し、その時点の状態を返します。
//
// この呼び出しはオペレーションの投函までで、完了は待ちません。完了を待つには
// 返された Name を PollVideo に渡してポーリングするか、veo パッケージを使ってください。
//
// 投函自体は SDK 内蔵のリトライに従って再送されます（レート制限や一時的な
// サーバーエラーで1本分の生成が落ちるのを防ぐため）。ポーリング側は逆にリトライを
// 打ち消します（PollVideo のコメント参照）。
func (c *Client) StartVideo(ctx context.Context, model string, req VideoRequest) (*VideoOperation, error) {
	if model == "" {
		return nil, ErrEmptyModelName
	}
	source, err := req.buildSource()
	if err != nil {
		return nil, err
	}
	config, err := req.buildConfig()
	if err != nil {
		return nil, err
	}

	op, err := c.videoClient.GenerateVideosFromSource(ctx, model, source, config)
	if err != nil {
		return nil, fmt.Errorf("動画生成オペレーションの開始に失敗しました: %w", err)
	}
	return videoOperationFrom(op)
}

// PollVideo は動画生成オペレーションの現在の状態を1回だけ問い合わせます。
//
// 意図的にリトライを挟まず、noRetryHTTPOptions でクライアント側の設定を打ち消します。
// ポーリング自体が繰り返しの仕組みなので、その内部でさらにバックオフを効かせると
// 1回の問い合わせに数十秒かかりうる二重の待ちになり、呼び出し側が設定した
// ポーリング間隔とタイムアウトが意味を失います。一時的な失敗を何回まで許容するかは、
// 間隔とタイムアウトを持っているループ側の判断です（veo.Client がその実装です）。
func (c *Client) PollVideo(ctx context.Context, operationName string) (*VideoOperation, error) {
	if strings.TrimSpace(operationName) == "" {
		return nil, ErrEmptyOperationName
	}
	op, err := c.videoClient.GetVideosOperation(ctx, &genai.GenerateVideosOperation{Name: operationName},
		&genai.GetOperationConfig{HTTPOptions: noRetryHTTPOptions()})
	if err != nil {
		return nil, fmt.Errorf("動画生成オペレーション %q の取得に失敗しました: %w", operationName, err)
	}
	return videoOperationFrom(op)
}

// buildSource は VideoRequest を genai の入力ソースへ変換し、SDK が受け付けない
// 組み合わせを検出します。
func (r VideoRequest) buildSource() (*genai.GenerateVideosSource, error) {
	hasImage := r.Image != nil && !r.Image.IsEmpty()
	hasVideo := r.Video != nil && !r.Video.IsEmpty()
	hasLastFrame := r.LastFrame != nil && !r.LastFrame.IsEmpty()
	hasRefs := len(r.References) > 0

	switch {
	case hasImage && hasVideo:
		return nil, fmt.Errorf("%w: Image と Video は同時に指定できません", ErrInvalidVideoInput)
	case hasRefs && (hasImage || hasVideo || hasLastFrame):
		return nil, fmt.Errorf("%w: References は Image / Video / LastFrame と同時に指定できません", ErrInvalidVideoInput)
	case hasLastFrame && !hasImage:
		return nil, fmt.Errorf("%w: LastFrame は Image（開始フレーム）とセットでのみ指定できます", ErrInvalidVideoInput)
	case r.Prompt == "" && !hasImage && !hasVideo:
		return nil, ErrEmptyPrompt
	}

	source := &genai.GenerateVideosSource{Prompt: r.Prompt}
	if hasImage {
		image, err := videoImage(r.Image, "Image")
		if err != nil {
			return nil, err
		}
		source.Image = image
	}
	if hasVideo {
		video, err := videoClip(r.Video)
		if err != nil {
			return nil, err
		}
		source.Video = video
	}
	return source, nil
}

// buildConfig は VideoRequest の生成パラメータを genai の設定へ変換します。
func (r VideoRequest) buildConfig() (*genai.GenerateVideosConfig, error) {
	seed, err := seedToPtrInt32(r.Seed)
	if err != nil {
		return nil, err
	}

	config := &genai.GenerateVideosConfig{
		OutputGCSURI:   r.OutputGCSURI,
		AspectRatio:    r.AspectRatio,
		Resolution:     r.Resolution,
		NegativePrompt: r.NegativePrompt,
		GenerateAudio:  r.GenerateAudio,
		Seed:           seed,
	}
	if r.DurationSec > 0 {
		config.DurationSeconds = new(int32(r.DurationSec))
	}
	if r.NumberOfVideos > 0 {
		config.NumberOfVideos = int32(r.NumberOfVideos)
	}
	if r.LastFrame != nil && !r.LastFrame.IsEmpty() {
		lastFrame, err := videoImage(r.LastFrame, "LastFrame")
		if err != nil {
			return nil, err
		}
		config.LastFrame = lastFrame
	}
	for i := range r.References {
		reference := r.References[i]
		if reference.Image.IsEmpty() {
			continue
		}
		image, err := videoImage(&reference.Image, fmt.Sprintf("References[%d].Image", i))
		if err != nil {
			return nil, err
		}
		config.ReferenceImages = append(config.ReferenceImages, &genai.VideoGenerationReferenceImage{
			Image:         image,
			ReferenceType: reference.Type,
		})
	}
	if len(r.ExtraBody) > 0 || r.ModifyRequestBody != nil {
		config.HTTPOptions = &genai.HTTPOptions{
			ExtraBody:             r.ExtraBody,
			ExtrasRequestProvider: r.ModifyRequestBody,
		}
	}
	return config, nil
}

// videoImage は Attachment を genai の画像入力へ変換します。field は、どの入力が
// 不正だったかをエラーで示すためのフィールド名です。
func videoImage(a *Attachment, field string) (*genai.Image, error) {
	if err := a.validateSource(ErrInvalidVideoInput, field); err != nil {
		return nil, err
	}
	return &genai.Image{
		GCSURI:     a.URI,
		ImageBytes: a.Data,
		MIMEType:   a.MIMEType,
	}, nil
}

// videoClip は Attachment を genai の動画入力へ変換します。
func videoClip(a *Attachment) (*genai.Video, error) {
	if err := a.validateSource(ErrInvalidVideoInput, "Video"); err != nil {
		return nil, err
	}
	return &genai.Video{
		URI:        a.URI,
		VideoBytes: a.Data,
		MIMEType:   a.MIMEType,
	}, nil
}

// videoOperationFrom は genai のオペレーションを公開型へ変換します。
func videoOperationFrom(op *genai.GenerateVideosOperation) (*VideoOperation, error) {
	if op == nil {
		return nil, newEmptyResponseError()
	}

	result := &VideoOperation{
		Name:    op.Name,
		Done:    op.Done,
		Failure: videoOperationFailure(op.Error),
	}
	if op.Response == nil {
		return result, nil
	}

	result.FilteredCount = op.Response.RAIMediaFilteredCount
	result.FilteredReasons = op.Response.RAIMediaFilteredReasons
	for _, generated := range op.Response.GeneratedVideos {
		if generated == nil || generated.Video == nil {
			continue
		}
		result.Videos = append(result.Videos, Attachment{
			URI:      generated.Video.URI,
			Data:     generated.Video.VideoBytes,
			MIMEType: generated.Video.MIMEType,
		})
	}
	return result, nil
}

// videoOperationFailure は、オペレーションのエラー情報を error へ変換します。
//
// genai はこのフィールドを map[string]any のまま公開しているため（google.rpc.Status
// 相当の任意構造）、既知のキーだけ拾って読める文字列に整えます。
func videoOperationFailure(raw map[string]any) error {
	if len(raw) == 0 {
		return nil
	}

	var parts []string
	if code, ok := raw["code"]; ok {
		parts = append(parts, fmt.Sprintf("code=%v", code))
	}
	if status, ok := raw["status"].(string); ok && status != "" {
		parts = append(parts, status)
	}
	if message, ok := raw["message"].(string); ok && message != "" {
		parts = append(parts, message)
	}
	if len(parts) == 0 {
		return fmt.Errorf("%w: %v", ErrVideoGenerationFailed, raw)
	}
	return fmt.Errorf("%w: %s", ErrVideoGenerationFailed, strings.Join(parts, ": "))
}
