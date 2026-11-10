// Package veo provides a client for video generation with Veo.
//
// Video generation is a long-running operation: after submitting, you have to keep polling until
// it completes. The gemini package owns each single round trip (StartVideo / PollVideo), and this
// package owns "how to wait" — the polling interval, the timeout, and how many transient failures
// to tolerate.
//
// Its only dependency is an injected gemini.VideoGenerator, and it never touches the genai SDK. In
// tests, passing a two-method fake lets you check the polling behaviour with neither GCP credentials nor HTTP.
package veo

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/internal/poll"
)

const (
	// DefaultPollInterval is the default interval for checking whether generation has completed.
	// Veo generation takes minutes, so shortening it only adds API calls.
	DefaultPollInterval = 10 * time.Second
	// DefaultPollTimeout is the default limit for waiting for completion.
	DefaultPollTimeout = 15 * time.Minute
	// DefaultMaxPollErrors is the default number of consecutive polling failures tolerated.
	// It keeps a transient error from losing an already generated video, while a permanent
	// outage is cut off without waiting pointlessly until the timeout.
	DefaultMaxPollErrors = 10
)

// Request is the input for one video generation.
//
// It is an alias of gemini.VideoRequest, so callers need not learn a separate type of this
// package to assemble the input. Whichever name is used, it is the same type.
type Request = gemini.VideoRequest

// Reference is one reference image passed to referenceImages (an alias of gemini.VideoReference).
type Reference = gemini.VideoReference

// Media is image or video input passed to video generation (an alias of gemini.Attachment).
// Set either a URI (gs:// on Vertex AI) or bytes.
type Media = gemini.Attachment

// Result is the result of a completed video generation.
type Result struct {
	// OperationName is the identifier of the operation used for generation.
	// It is kept so it can be logged for tracking billing and failures.
	OperationName string
	// Videos are the generated videos. URI is set if Request.OutputGCSURI was specified,
	// otherwise Data.
	Videos []gemini.Attachment
	// FilteredCount is the number of videos excluded by safety policy. If not a single one was
	// generated, Generate returns an error, so a non-zero value here with non-empty Videos is
	// the case where several were requested and only some were excluded.
	FilteredCount int32
	// FilteredReasons are the reasons for the exclusions.
	FilteredReasons []string
}

// First returns the first generated video. In the usual case of requesting just one, it saves
// writing an index access and bounds check every time.
// It returns false if there is no video.
func (r *Result) First() (gemini.Attachment, bool) {
	if r == nil || len(r.Videos) == 0 {
		return gemini.Attachment{}, false
	}
	return r.Videos[0], true
}

// Client handles video generation from submission to waiting for completion.
type Client struct {
	generator     gemini.VideoGenerator
	pollInterval  time.Duration
	pollTimeout   time.Duration
	maxPollErrors int
	logger        *slog.Logger
}

// New initialises a Client with an injected video generation client.
//
// A *gemini.Client can be passed as generator as is.
//
//	gc, err := gemini.New(ctx, cfg)
//	vc, err := veo.New(gc, veo.WithPollInterval(10*time.Second))
func New(generator gemini.VideoGenerator, opts ...Option) (*Client, error) {
	if generator == nil {
		return nil, ErrGeneratorRequired
	}
	c := &Client{
		generator:     generator,
		pollInterval:  DefaultPollInterval,
		pollTimeout:   DefaultPollTimeout,
		maxPollErrors: DefaultMaxPollErrors,
		logger:        slog.Default(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Generate starts video generation, waits until it completes, and returns the result.
//
// If generation itself fails (such as a block by safety policy), the error includes
// gemini.ErrVideoGenerationFailed. If completion could not be waited for, it returns the
// reason polling was abandoned (a timeout or ErrPollFailed).
func (c *Client) Generate(ctx context.Context, model string, req Request) (*Result, error) {
	op, err := c.start(ctx, model, req)
	if err != nil {
		return nil, err
	}

	// If the submission response is already complete, skip one poll and return it directly.
	if op.Done {
		return resultFrom(op)
	}
	if strings.TrimSpace(op.Name) == "" {
		return nil, ErrMissingOperationName
	}
	return c.Wait(ctx, op.Name)
}

// Submit starts video generation and returns the operation name without waiting for completion.
//
// It is the counterpart entry point to Wait. On a job platform with a cap on execution time,
// you can submit, return for now, and resume waiting in the next execution by passing the name.
// Without it, the submitting side alone would call gemini.StartVideo directly instead of going
// through veo, splitting the dependency between submitting and waiting.
//
// The returned name can be passed to Wait as is.
func (c *Client) Submit(ctx context.Context, model string, req Request) (string, error) {
	op, err := c.start(ctx, model, req)
	if err != nil {
		return "", err
	}
	// Return the name even if already complete. Wait can fetch the result with a single poll.
	if strings.TrimSpace(op.Name) == "" {
		return "", ErrMissingOperationName
	}
	return op.Name, nil
}

// start submits video generation, refuses a missing response, and returns the operation.
func (c *Client) start(ctx context.Context, model string, req Request) (*gemini.VideoOperation, error) {
	op, err := c.generator.StartVideo(ctx, model, req)
	if err != nil {
		return nil, err
	}
	if op == nil {
		return nil, fmt.Errorf("veo: %w", gemini.ErrEmptyResponse)
	}
	c.logger.InfoContext(ctx, "started video generation operation", "operation", op.Name, "model", model)
	return op, nil
}

// Wait polls a started video generation operation until it completes and returns the result.
//
// It is separate from Generate so that submitting and waiting for completion can happen in
// different processes or executions (on a job platform with a cap on execution time, you can
// submit, return for now, and resume waiting in the next execution by passing the name).
//
// The way of waiting belongs to internal/poll. The first query is made immediately without
// waiting for the interval (when resuming, the operation has often already completed, and
// checking only after one interval — 10 seconds by default — is pure dead time), and the time
// limit applies to the in-flight query too.
//
// Individual queries are not retried. Transient failures are tolerated up to maxPollErrors
// times, and beyond that the wait is abandoned with ErrPollFailed
// (see the comment on gemini.PollVideo).
func (c *Client) Wait(ctx context.Context, operationName string) (*Result, error) {
	if strings.TrimSpace(operationName) == "" {
		return nil, ErrMissingOperationName
	}

	loop := poll.Loop{
		Interval:        c.pollInterval,
		Timeout:         c.pollTimeout,
		MaxErrors:       c.maxPollErrors,
		Subject:         fmt.Sprintf("operation %q", operationName),
		RepeatedFailure: ErrPollFailed,
		OnTransientError: func(err error, consecutive int) {
			c.logger.WarnContext(ctx, "checking video generation status failed; checking again",
				"operation", operationName, "consecutive_errors", consecutive, "error", err)
		},
	}

	op, err := loop.Run(ctx, func(pollCtx context.Context) (*gemini.VideoOperation, bool, error) {
		op, err := c.generator.PollVideo(pollCtx, operationName)
		if err != nil {
			return nil, false, err
		}
		return op, op.Done, nil
	})
	if err != nil {
		return nil, err
	}
	return resultFrom(op)
}

// resultFrom converts a completed operation into a result.
func resultFrom(op *gemini.VideoOperation) (*Result, error) {
	if op.Failure != nil {
		return nil, fmt.Errorf("operation %q: %w", op.Name, op.Failure)
	}
	if len(op.Videos) == 0 {
		return nil, noVideoError(op)
	}
	return &Result{
		OperationName:   op.Name,
		Videos:          op.Videos,
		FilteredCount:   op.FilteredCount,
		FilteredReasons: op.FilteredReasons,
	}, nil
}

// noVideoError builds the error for when the operation succeeded but no video came back.
// Exclusion by safety policy accounts for most of this path, so when the reason is known it
// is included in the message (it is the information needed to fix the prompt).
func noVideoError(op *gemini.VideoOperation) error {
	if op.FilteredCount > 0 || len(op.FilteredReasons) > 0 {
		return fmt.Errorf("%w: excluded by safety policy (count=%d, reasons=%s)",
			ErrNoVideoGenerated, op.FilteredCount, strings.Join(op.FilteredReasons, "; "))
	}
	return fmt.Errorf("%w: operation %q", ErrNoVideoGenerated, op.Name)
}
