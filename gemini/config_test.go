package gemini

import (
	"errors"
	"math"
	"net/http"
	"testing"
	"time"

	"cloud.google.com/go/auth/credentials"
	"google.golang.org/genai"
)

// skipWithoutGCPCredentials skips the test in environments where Application Default
// Credentials are not available (such as CI runners).
//
// genai's UseDefaultCredentials detects ADC with the cloud-platform scope, so this calls the
// same detector with the same scope to check only the precondition.
func skipWithoutGCPCredentials(t *testing.T) {
	t.Helper()

	_, err := credentials.DetectDefault(&credentials.DetectOptions{
		Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
	})
	if err != nil {
		t.Skipf("skipping this test because ADC was not found: %v", err)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want error
	}{
		{
			name: "valid: both ProjectID and LocationID set",
			cfg:  Config{ProjectID: "my-project", LocationID: "asia-northeast1"},
			want: nil,
		},
		{
			// The mistake of forgetting to pass the config.
			name: "invalid: both empty",
			cfg:  Config{},
			want: ErrConfigRequired,
		},
		{
			// The different mistake of one environment variable being empty.
			name: "invalid: ProjectID only",
			cfg:  Config{ProjectID: "my-project"},
			want: ErrIncompleteVertexConfig,
		},
		{
			name: "invalid: LocationID only",
			cfg:  Config{LocationID: "asia-northeast1"},
			want: ErrIncompleteVertexConfig,
		},
		{
			name: "valid: APIKey only",
			cfg:  Config{APIKey: "key"},
			want: nil,
		},
		{
			// There is no way to decide which to use, so fail rather than silently picking one.
			name: "invalid: APIKey together with Vertex AI",
			cfg:  Config{ProjectID: "my-project", LocationID: "asia-northeast1", APIKey: "key"},
			want: ErrExclusiveConfig,
		},
		{
			// Report the combination itself before a half-written Vertex config.
			name: "invalid: APIKey with a half-written Vertex config",
			cfg:  Config{ProjectID: "my-project", APIKey: "key"},
			want: ErrExclusiveConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if tt.want == nil {
				if err != nil {
					t.Fatalf("validate() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Errorf("validate() error = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestConfigToClientConfig checks that a client config is built from the configuration.
// Only when APIKey is specified does it switch to the Gemini API backend.
func TestConfigToClientConfig(t *testing.T) {
	t.Run("Vertex AI", func(t *testing.T) {
		got, err := Config{ProjectID: "proj-v", LocationID: "loc-v"}.toClientConfig()
		if err != nil {
			t.Fatalf("toClientConfig() error = %v", err)
		}

		if got.Project != "proj-v" || got.Location != "loc-v" {
			t.Errorf("project/location = %q/%q", got.Project, got.Location)
		}
		if got.Backend != genai.BackendVertexAI {
			t.Errorf("Backend = %v, want Vertex AI", got.Backend)
		}
		// No HTTPClient was passed, so ADC detection is left to the SDK.
		if got.HTTPClient != nil {
			t.Errorf("HTTPClient = %+v, want nil (left to the SDK default)", got.HTTPClient)
		}
		if got.HTTPOptions.RetryOptions == nil {
			t.Error("RetryOptions are not on the client config")
		}
	})

	t.Run("Gemini API", func(t *testing.T) {
		got, err := Config{APIKey: "key"}.toClientConfig()
		if err != nil {
			t.Fatalf("toClientConfig() error = %v", err)
		}

		if got.APIKey != "key" {
			t.Errorf("APIKey = %q", got.APIKey)
		}
		if got.Backend != genai.BackendGeminiAPI {
			t.Errorf("Backend = %v, want Gemini API", got.Backend)
		}
		// Vertex AI fields must not be included; they would confuse the SDK's decision.
		if got.Project != "" || got.Location != "" {
			t.Errorf("project/location = %q/%q, want both empty", got.Project, got.Location)
		}
		if got.HTTPOptions.RetryOptions == nil {
			t.Error("RetryOptions are not on the client config")
		}
	})
}

// TestToClientConfigKeepsSuppliedHTTPClientForAPIKey checks that passing an HTTPClient on the
// API key path does not try to attach ADC.
//
// Gemini API authentication adds a key header and does not depend on the Transport. Calling
// UseDefaultCredentials here would make creating the client itself fail in environments without
// ADC (which is exactly what an environment holding only an API key is).
func TestToClientConfigKeepsSuppliedHTTPClientForAPIKey(t *testing.T) {
	supplied := &http.Client{Timeout: 42 * time.Second}

	got, err := Config{APIKey: "key", HTTPClient: supplied}.toClientConfig()
	if err != nil {
		t.Fatalf("toClientConfig() error = %v", err)
	}

	if got.HTTPClient == nil {
		t.Fatal("HTTPClient is not on the client config")
	}
	if got.HTTPClient == supplied {
		t.Error("the caller's instance is used as is (it should be copied)")
	}
	if got.HTTPClient.Timeout != supplied.Timeout {
		t.Errorf("Timeout = %v, want %v", got.HTTPClient.Timeout, supplied.Timeout)
	}
	if got.HTTPClient.Transport != nil {
		t.Errorf("Transport = %+v, want nil (no credentials are attached on the API key path)", got.HTTPClient.Transport)
	}
}

func TestConfigRetryOptions(t *testing.T) {
	t.Run("applies the defaults", func(t *testing.T) {
		got := Config{}.retryOptions()
		if got == nil {
			t.Fatal("retryOptions() = nil, want a config with defaults")
		}
		if *got.Attempts != int32(DefaultMaxRetries)+1 {
			t.Errorf("Attempts = %v, want %v (total attempts including the first)", *got.Attempts, DefaultMaxRetries+1)
		}
		if *got.InitialDelay != DefaultInitialDelay.Seconds() || *got.MaxDelay != DefaultMaxDelay.Seconds() {
			t.Errorf("the default delays are not applied: %+v", got)
		}
	})

	t.Run("overridden by the configured values", func(t *testing.T) {
		got := Config{
			MaxRetries:   5,
			InitialDelay: 10 * time.Second,
			MaxDelay:     60 * time.Second,
		}.retryOptions()

		if *got.Attempts != 6 {
			t.Errorf("Attempts = %v, want 6 (first attempt + 5 retries)", *got.Attempts)
		}
		if *got.InitialDelay != 10 || *got.MaxDelay != 60 {
			t.Errorf("the delays are not applied correctly: %+v", got)
		}
	})

	// The SDK's default jitter (adding U(0, 1s)) has almost no effect when InitialDelay is tens of
	// seconds, so a width proportional to the initial delay is set explicitly.
	t.Run("jitter is half the initial delay", func(t *testing.T) {
		got := Config{InitialDelay: 60 * time.Second}.retryOptions()
		if *got.Jitter != 30 {
			t.Errorf("Jitter = %v, want 30", *got.Jitter)
		}
	})

	// nil means "run exactly once" to the SDK. A value-typed MaxRetries cannot tell the zero value
	// apart from "do not retry", so a dedicated flag expresses it.
	t.Run("DisableRetry gives nil", func(t *testing.T) {
		if got := (Config{DisableRetry: true}).retryOptions(); got != nil {
			t.Errorf("retryOptions() = %+v, want nil", got)
		}
	})

	t.Run("a MaxRetries of 0 counts as unset and falls back to the default", func(t *testing.T) {
		got := Config{MaxRetries: 0}.retryOptions()
		if *got.Attempts != int32(DefaultMaxRetries)+1 {
			t.Errorf("Attempts = %v, want %v", *got.Attempts, DefaultMaxRetries+1)
		}
	})
}

// TestAttemptsFromSaturates checks that a retry count that does not fit in int32 does not wrap
// the total attempt count around to negative. If it wrapped, the SDK would not run even once.
func TestAttemptsFromSaturates(t *testing.T) {
	tests := []struct {
		name       string
		maxRetries uint
		want       int32
	}{
		{"a normal value gets +1", 3, 4},
		{"a value beyond the int32 maximum saturates", math.MaxUint32, math.MaxInt32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := attemptsFrom(tt.maxRetries); got != tt.want {
				t.Errorf("attemptsFrom(%d) = %d, want %d", tt.maxRetries, got, tt.want)
			}
		})
	}
}

// TestNoRetryHTTPOptions checks the setting that cancels out retries per request.
// genai replaces the client config with the request's RetryOptions only when they are non-nil,
// so cancelling them out requires passing Attempts=1 rather than nil.
func TestNoRetryHTTPOptions(t *testing.T) {
	got := noRetryHTTPOptions()
	if got.RetryOptions == nil || *got.RetryOptions.Attempts != 1 {
		t.Errorf("noRetryHTTPOptions() = %+v, want Attempts=1", got.RetryOptions)
	}
}

// TestToClientConfigAttachesCredentialsToSuppliedHTTPClient checks that Vertex AI authentication
// works even when an HTTPClient is specified.
//
// When ClientConfig.HTTPClient is non-nil, genai skips ADC detection and uses the passed client
// without an auth header. So passing a plain &http.Client{Timeout: ...} makes every request fail
// with 401 (CREDENTIALS_MISSING). This actually took production down, so the test checks that the
// Transport has been replaced (that is, that authentication is attached).
func TestToClientConfigAttachesCredentialsToSuppliedHTTPClient(t *testing.T) {
	skipWithoutGCPCredentials(t)

	supplied := &http.Client{Timeout: 42 * time.Second}
	cfg := Config{ProjectID: "p", LocationID: "asia-northeast1", HTTPClient: supplied}

	got, err := cfg.toClientConfig()
	if err != nil {
		t.Fatalf("toClientConfig() error = %v", err)
	}
	if got.HTTPClient == nil {
		t.Fatal("HTTPClient = nil, want the client that was passed")
	}
	if got.HTTPClient.Timeout != 42*time.Second {
		t.Errorf("Timeout = %v, want the specified value kept", got.HTTPClient.Timeout)
	}
	if got.HTTPClient.Transport == nil {
		t.Error("Transport = nil, want the auth middleware attached")
	}
	// The caller's instance is not modified (it may be reused elsewhere).
	if supplied.Transport != nil {
		t.Error("the caller's http.Client was modified; it should be copied")
	}
	if got.HTTPClient == supplied {
		t.Error("HTTPClient is the caller's instance itself; it should be copied")
	}
}

// Billing errors such as 402 have different causes depending on the path, so the path name in the error message must not be mixed up.
func TestConfigBackendName(t *testing.T) {
	if got := (Config{APIKey: "k"}).backendName(); got != "Gemini API" {
		t.Errorf("APIKey: got %q, want Gemini API", got)
	}
	if got := (Config{ProjectID: "p", LocationID: "l"}).backendName(); got != "Vertex AI" {
		t.Errorf("Vertex: got %q, want Vertex AI", got)
	}
}
