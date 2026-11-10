package gemini

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"
)

// VideoGenerator is the minimal interface for starting a long-running video generation
// operation and checking its progress.
//
// How to wait for completion (polling interval, timeout, number of tolerated transient errors)
// is not included. That is the policy of the loop calling these two, and not a reason to swap
// the implementation. The veo package takes this interface and builds the loop.
type VideoGenerator interface {
	StartVideo(ctx context.Context, model string, req VideoRequest) (*VideoOperation, error)
	PollVideo(ctx context.Context, operationName string) (*VideoOperation, error)
}

// VideoReferenceType is how an image passed to referenceImages is used
// (an alias of genai.VideoGenerationReferenceType).
type VideoReferenceType = genai.VideoGenerationReferenceType

// Reference image types.
const (
	// VideoReferenceAsset is a reference for making the subject in the image (a character, a prop,
	// etc.) appear in the video. Up to three can be specified.
	VideoReferenceAsset VideoReferenceType = genai.VideoGenerationReferenceTypeAsset
	// VideoReferenceStyle is a reference for carrying the image's art style into the video.
	VideoReferenceStyle VideoReferenceType = genai.VideoGenerationReferenceTypeStyle
)

// VideoReference is one reference image passed to referenceImages.
type VideoReference struct {
	// Image is the referenced image. Specify a URI (gs:// on Vertex AI) or bytes.
	Image Attachment
	// Type is how this image is used. Unspecified leaves it to the API default.
	Type VideoReferenceType
}

// VideoRequest is the input for one video generation.
//
// The three video input modes, Image / Video / References, are mutually exclusive, and the
// combinations a model can interpret are limited (Veo cannot combine video with image / referenceImages).
// Which combination to choose is up to the caller, but combinations the SDK is certain to reject
// are refused by StartVideo with ErrInvalidVideoInput before sending.
type VideoRequest struct {
	// Prompt is the generation instruction. It is required when there is neither Image nor Video,
	// and the API also requires it when References are used.
	Prompt string
	// Image is the start frame of the video (image-to-video).
	Image *Attachment
	// Video is the input video for an extension (video extension). Mutually exclusive with Image.
	Video *Attachment
	// LastFrame is the last frame of the video (first/last frame interpolation).
	// The API only accepts it together with Image, so it cannot be specified without Image.
	LastFrame *Attachment
	// References are reference images for the subject or art style. Mutually exclusive with Image / Video / LastFrame.
	References []VideoReference

	// DurationSec is the length of the generated video in seconds. 0 leaves it to the API default.
	// The accepted values depend on the model and the combination of inputs.
	DurationSec int
	// Seed is the random seed for reproducible generation. nil means random every time.
	// A value outside the int32 range results in ErrInvalidSeed.
	Seed *int64
	// AspectRatio is the aspect ratio, such as "16:9" / "9:16".
	AspectRatio string
	// Resolution is the resolution, such as "720p" / "1080p".
	Resolution string
	// NegativePrompt specifies elements that should not appear in the generation.
	NegativePrompt string
	// GenerateAudio is whether audio is generated at the same time. nil leaves it to the API default.
	GenerateAudio *bool
	// OutputGCSURI is the GCS bucket where results are stored. If not specified, the result is
	// returned as bytes (it is usually specified, since responses get large for long videos).
	OutputGCSURI string
	// NumberOfVideos is the number of videos to generate. 0 means the API default (usually one).
	NumberOfVideos int

	// ExtraBody is extra fields inserted into the request body.
	//
	// It is an escape hatch for using preview features the SDK has no type for yet, and its
	// structure must match the backend's REST API. For items the SDK supports, use the fields
	// above (ExtraBody gets neither type checking nor validation).
	//
	// Merging only recurses when both sides are maps. When the value for the same key is an array,
	// it does not recurse and the array is replaced wholesale, so it cannot be used to add values to
	// an array such as instances in a Vertex AI request (the prompt and image input would be erased
	// with it). To insert into array elements, use ModifyRequestBody.
	ExtraBody map[string]any

	// ModifyRequestBody rewrites the assembled request body just before it is sent.
	// It is called with the whole body after the ExtraBody merge is done.
	//
	// It is the means of inserting values where ExtraBody cannot reach (such as inside array elements).
	// The body's structure is the backend's REST API itself, with neither type checking nor validation.
	// It may modify the received map directly and return it.
	ModifyRequestBody func(body map[string]any) map[string]any
}

// VideoOperation is the state of a long-running video generation operation.
//
// Whether generation has completed is determined by Done. If Done and Failure is non-nil,
// generation itself failed (fetching the operation itself succeeded, so StartVideo / PollVideo
// return no error and put it in this field instead).
type VideoOperation struct {
	// Name is the operation's identifier. Pass it to PollVideo to check progress.
	Name string
	// Done is whether generation has completed (successfully or not).
	Done bool
	// Videos are the generated videos. URI is set if OutputGCSURI was specified,
	// otherwise Data.
	Videos []Attachment
	// FilteredCount is the number of videos excluded by safety policy.
	FilteredCount int32
	// FilteredReasons are the reasons for the exclusions.
	FilteredReasons []string
	// Failure is the reason generation failed. nil on success.
	Failure error
}

// StartVideo starts a long-running video generation operation and returns its state at that point.
//
// This call only submits the operation and does not wait for completion. To wait, pass the
// returned Name to PollVideo and poll, or use the veo package.
//
// The submission itself is resent according to the SDK's built-in retries (to keep rate limits or
// transient server errors from losing a whole video's generation). The polling side, conversely,
// cancels retries out (see the comment on PollVideo).
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
		return nil, fmt.Errorf("failed to start video generation operation: %w", err)
	}
	return videoOperationFrom(op)
}

// PollVideo queries the current state of a video generation operation exactly once.
//
// It deliberately does not retry, cancelling out the client's settings with noRetryHTTPOptions.
// Polling is itself a repetition mechanism, so applying backoff inside it would create a double
// wait in which a single query could take tens of seconds, making the polling interval and
// timeout the caller set meaningless. How many transient failures to tolerate is up to the loop
// that holds the interval and timeout (veo.Client is that implementation).
func (c *Client) PollVideo(ctx context.Context, operationName string) (*VideoOperation, error) {
	if strings.TrimSpace(operationName) == "" {
		return nil, ErrEmptyOperationName
	}
	op, err := c.videoClient.GetVideosOperation(ctx, &genai.GenerateVideosOperation{Name: operationName},
		&genai.GetOperationConfig{HTTPOptions: noRetryHTTPOptions()})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch video generation operation %q: %w", operationName, err)
	}
	return videoOperationFrom(op)
}

// buildSource converts a VideoRequest into genai's input source and detects combinations the
// SDK does not accept.
func (r VideoRequest) buildSource() (*genai.GenerateVideosSource, error) {
	hasImage := r.Image != nil && !r.Image.IsEmpty()
	hasVideo := r.Video != nil && !r.Video.IsEmpty()
	hasLastFrame := r.LastFrame != nil && !r.LastFrame.IsEmpty()
	hasRefs := len(r.References) > 0

	switch {
	case hasImage && hasVideo:
		return nil, fmt.Errorf("%w: Image and Video cannot be specified together", ErrInvalidVideoInput)
	case hasRefs && (hasImage || hasVideo || hasLastFrame):
		return nil, fmt.Errorf("%w: References cannot be specified together with Image / Video / LastFrame", ErrInvalidVideoInput)
	case hasLastFrame && !hasImage:
		return nil, fmt.Errorf("%w: LastFrame can only be specified together with Image (the start frame)", ErrInvalidVideoInput)
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

// buildConfig converts a VideoRequest's generation parameters into genai's config.
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

// videoImage converts an Attachment into genai's image input. field is the field name used
// in the error to show which input was invalid.
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

// videoClip converts an Attachment into genai's video input.
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

// videoOperationFrom converts a genai operation into the public type.
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

// videoOperationFailure converts an operation's error information into an error.
//
// genai exposes this field as a raw map[string]any (an arbitrary structure equivalent to
// google.rpc.Status), so only the known keys are picked up and formatted into a readable string.
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
