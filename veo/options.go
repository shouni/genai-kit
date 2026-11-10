package veo

import (
	"log/slog"
	"time"
)

// Option is a function that applies a Client setting.
// Invalid values (zero or below) are ignored as "not specified" and the default is kept.
type Option func(*Client)

// WithPollInterval sets the interval at which generation completion is checked.
func WithPollInterval(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.pollInterval = d
		}
	}
}

// WithPollTimeout sets the maximum time to wait for generation to complete.
// If the caller's context has a shorter deadline, that one takes effect first.
func WithPollTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.pollTimeout = d
		}
	}
}

// WithMaxPollErrors sets how many consecutive failures of the generation status check
// are tolerated. On reaching this count, waiting is abandoned with ErrPollFailed.
// A single success along the way resets the count to zero.
func WithMaxPollErrors(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.maxPollErrors = n
		}
	}
}

// WithLogger sets where this client's logs are written.
// If unspecified (or nil), slog.Default() is used. Passing a logger with attributes such
// as a job ID attaches those attributes to the polling warning logs too.
func WithLogger(logger *slog.Logger) Option {
	return func(c *Client) {
		if logger != nil {
			c.logger = logger
		}
	}
}
