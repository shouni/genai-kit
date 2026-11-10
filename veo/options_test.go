package veo

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

// TestOptionsIgnoreInvalidValues checks that invalid values (zero or below, nil) are
// ignored as "not specified" and the defaults are kept. This rule keeps an unset zero
// value in a caller's assembled settings from turning into "a runaway loop with interval 0".
func TestOptionsIgnoreInvalidValues(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		name string
		opts []Option
		want Client
	}{
		{
			name: "keeps the default when unspecified",
			opts: nil,
			want: Client{pollInterval: DefaultPollInterval, pollTimeout: DefaultPollTimeout, maxPollErrors: DefaultMaxPollErrors},
		},
		{
			name: "applies the specified value",
			opts: []Option{WithPollInterval(3 * time.Second), WithPollTimeout(time.Minute), WithMaxPollErrors(5)},
			want: Client{pollInterval: 3 * time.Second, pollTimeout: time.Minute, maxPollErrors: 5},
		},
		{
			name: "ignores zero or below and keeps the default",
			opts: []Option{WithPollInterval(0), WithPollTimeout(-time.Second), WithMaxPollErrors(0)},
			want: Client{pollInterval: DefaultPollInterval, pollTimeout: DefaultPollTimeout, maxPollErrors: DefaultMaxPollErrors},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(&fakeGenerator{}, append(tt.opts, WithLogger(logger))...)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if got.pollInterval != tt.want.pollInterval {
				t.Errorf("pollInterval = %v, want %v", got.pollInterval, tt.want.pollInterval)
			}
			if got.pollTimeout != tt.want.pollTimeout {
				t.Errorf("pollTimeout = %v, want %v", got.pollTimeout, tt.want.pollTimeout)
			}
			if got.maxPollErrors != tt.want.maxPollErrors {
				t.Errorf("maxPollErrors = %d, want %d", got.maxPollErrors, tt.want.maxPollErrors)
			}
		})
	}
}

// TestWithLoggerIgnoresNil checks that a nil logger does not erase the default logger.
// If it did, the polling warning logs would disappear and a nil dereference would crash.
func TestWithLoggerIgnoresNil(t *testing.T) {
	client, err := New(&fakeGenerator{}, WithLogger(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if client.logger == nil {
		t.Error("logger = nil, want slog.Default()")
	}
}
