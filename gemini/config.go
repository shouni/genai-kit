package gemini

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"google.golang.org/genai"
	"google.golang.org/genai/interactions/retry"
)

var (
	// ErrConfigRequired is returned when neither ProjectID/LocationID nor APIKey is set.
	ErrConfigRequired = errors.New("gemini: either ProjectID/LocationID or APIKey is required")
	// ErrExclusiveConfig is returned when ProjectID/LocationID and APIKey are set at the same time.
	ErrExclusiveConfig = errors.New("gemini: ProjectID/LocationID and APIKey are mutually exclusive")
	// ErrIncompleteVertexConfig is returned when only one of ProjectID and LocationID is set.
	ErrIncompleteVertexConfig = errors.New("gemini: Vertex AI requires both ProjectID and LocationID")
)

const (
	// DefaultMaxRetries is the default used when the retry count is unset.
	DefaultMaxRetries uint = 1
	// DefaultInitialDelay is the default used when the initial retry delay is unset.
	DefaultInitialDelay time.Duration = 30 * time.Second
	// DefaultMaxDelay is the default used when the maximum retry delay is unset.
	DefaultMaxDelay time.Duration = 120 * time.Second
)

// Config is the configuration for initialisation.
//
// Normally Vertex AI is used: specify ProjectID and LocationID, and authentication follows
// Application Default Credentials (ADC). APIKey is a temporary exception for models that
// cannot be used that way.
type Config struct {
	ProjectID  string // Google Cloud Project ID
	LocationID string // Location (e.g., "us-central1")

	// APIKey is the key for using the Gemini API (Google AI Studio) backend.
	// It is mutually exclusive with ProjectID/LocationID, and setting it switches the backend.
	//
	// It is a temporary exception. This library's backend has been consolidated on Vertex AI, and
	// the value lies in callers not having to know which one they are using. It remains only
	// because the latest Lyria is not offered on Vertex AI, so music generation alone runs only on
	// the API key path. Once Lyria is available on Vertex AI, delete this field together with the
	// branches in validate / toClientConfig.
	APIKey string

	// MaxRetries is the number of retries allowed for one call (the first attempt is not counted).
	// 0 means unset, and DefaultMaxRetries is used. To stop retrying, set
	// DisableRetry.
	MaxRetries uint

	// DisableRetry disables retries entirely and runs exactly once.
	// Use it for calls that cause a side effect on every success, where resending after a lost
	// response would mean running twice.
	DisableRetry bool

	InitialDelay time.Duration
	MaxDelay     time.Duration

	// RequestTimeout is the time limit for one generation call (including retries).
	// 0 means unlimited, following only the deadline of the caller's context.
	// It does not apply to waiting for video generation to complete (veo's settings handle that).
	RequestTimeout time.Duration

	// HTTPClient replaces the HTTP client used by the genai SDK.
	// If nil, the SDK's default is used.
	//
	// Specify it to control timeouts or proxies, or to use a client hardened
	// against SSRF.
	//
	//	cfg.HTTPClient = securenet.NewSafeHTTPClient(60 * time.Second)
	//
	// You need not worry about authentication. When given an HTTPClient, genai skips ADC detection
	// and sends without an auth header, but toClientConfig re-attaches the credentials. The instance
	// you passed is not modified; a copy is used.
	HTTPClient *http.Client
}

// validate checks that the configuration is correct.
//
// Unset (both empty) and half-written (only one set) are kept apart because the former is the
// mistake of "forgetting to pass the config" and the latter of "one environment variable was empty".
//
// When both APIKey and Vertex AI settings arrive, there is no way to decide which to use, so
// it returns an error rather than silently picking one.
func (c Config) validate() error {
	hasVertexField := c.ProjectID != "" || c.LocationID != ""

	if hasVertexField && c.APIKey != "" {
		return ErrExclusiveConfig
	}
	if !hasVertexField {
		if c.APIKey != "" {
			return nil
		}
		return ErrConfigRequired
	}
	if c.ProjectID == "" || c.LocationID == "" {
		return ErrIncompleteVertexConfig
	}
	return nil
}

// usesAPIKey reports whether the configuration uses the Gemini API backend.
// For a validated Config, having APIKey set is the condition itself.
func (c Config) usesAPIKey() bool {
	return c.APIKey != ""
}

// toClientConfig converts Config into a genai.ClientConfig.
//
// When HTTPClient is specified, it also attaches credentials. When ClientConfig.HTTPClient is
// non-nil, genai skips ADC detection altogether and uses the passed client without an auth
// header, so without this every request would fail with 401 (CREDENTIALS_MISSING). In other
// words, passing a plain &http.Client{Timeout: ...} would throw away authentication while
// seeming to set a timeout.
func (c Config) toClientConfig() (*genai.ClientConfig, error) {
	cc := &genai.ClientConfig{}
	cc.HTTPOptions.RetryOptions = c.retryOptions()
	if c.usesAPIKey() {
		cc.APIKey = c.APIKey
		cc.Backend = genai.BackendGeminiAPI
	} else {
		cc.Project = c.ProjectID
		cc.Location = c.LocationID
		cc.Backend = genai.BackendVertexAI
	}

	if c.HTTPClient == nil {
		return cc, nil
	}
	// UseDefaultCredentials rewrites the passed client's Transport, so swap in a shallow copy
	// to avoid touching the instance the caller holds.
	// Settings such as Timeout carry over.
	clone := *c.HTTPClient
	cc.HTTPClient = &clone

	// Gemini API authentication adds an API key header and does not depend on the Transport.
	if c.usesAPIKey() {
		return cc, nil
	}
	if err := cc.UseDefaultCredentials(); err != nil {
		return nil, fmt.Errorf("gemini: failed to attach credentials to the provided HTTPClient: %w", err)
	}
	return cc, nil
}

// orDefault returns v if it is positive, otherwise def.
func orDefault(v, def time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return def
}

// retryOptions converts Config into the configuration for the genai SDK's built-in retries.
// Returning nil makes the SDK run once without retrying.
//
// Which statuses are retried (408/429/5xx) and how transport errors are judged is left to the SDK.
// Copying the list would leave us holding a stale list when the SDK widens its criteria.
func (c Config) retryOptions() *genai.HTTPRetryOptions {
	if c.DisableRetry {
		return nil
	}

	maxRetries := c.MaxRetries
	if maxRetries == 0 {
		maxRetries = DefaultMaxRetries
	}
	initialDelay := orDefault(c.InitialDelay, DefaultInitialDelay)
	maxDelay := orDefault(c.MaxDelay, DefaultMaxDelay)

	return &genai.HTTPRetryOptions{
		Attempts:     new(attemptsFrom(maxRetries)),
		InitialDelay: new(initialDelay.Seconds()),
		MaxDelay:     new(maxDelay.Seconds()),
		// The SDK's default adds U(0, 1s), which has almost no effect at intervals of tens of seconds.
		// When parallel generations are refused with the same 429, only jitter spreads them out (callguard's
		// firing interval does not apply to retries), so the jitter is given a width proportional to the delay.
		Jitter: new(initialDelay.Seconds() / 2),
	}
}

// backendName returns the name of the path this configuration connects through.
func (c Config) backendName() string {
	if c.APIKey != "" {
		return "Gemini API"
	}
	return "Vertex AI"
}

// interactionRetryConfig maps the same policy as retryOptions onto the Interactions retry configuration.
//
// genai's Interactions client does not read HTTPRetryOptions and, unless told otherwise, resends with
// its own defaults (4 times, 500ms to 8s). To avoid Config's MaxRetries and DisableRetry applying to
// only one of the paths, they are aligned here. With DisableRetry it returns "none" rather than nil,
// because nil means "use the defaults".
func (c Config) interactionRetryConfig() *retry.Config {
	if c.DisableRetry {
		return &retry.Config{Strategy: "none"}
	}

	maxRetries := c.MaxRetries
	if maxRetries == 0 {
		maxRetries = DefaultMaxRetries
	}
	return &retry.Config{
		Strategy: "attempt-count-backoff",
		Backoff: &retry.BackoffStrategy{
			InitialInterval: int(orDefault(c.InitialDelay, DefaultInitialDelay).Milliseconds()),
			MaxInterval:     int(orDefault(c.MaxDelay, DefaultMaxDelay).Milliseconds()),
			Exponent:        2,
		},
		RetryConnectionErrors: true,
		MaxRetries:            new(int(maxRetries)),
	}
}

// noRetryHTTPOptions cancels out the client's retry settings for a single request.
// It is used on paths where the caller owns the interval and timeout, such as polling.
// It passes "exactly once" rather than nil because genai only applies a request's RetryOptions
// as an override when they are non-nil.
func noRetryHTTPOptions() *genai.HTTPOptions {
	return &genai.HTTPOptions{RetryOptions: &genai.HTTPRetryOptions{Attempts: new(int32(1))}}
}

// attemptsFrom converts the retry count into genai's total attempt count (including the first).
// A value that does not fit in int32 saturates.
func attemptsFrom(maxRetries uint) int32 {
	if uint64(maxRetries) >= math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(maxRetries) + 1
}
