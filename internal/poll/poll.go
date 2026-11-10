// Package poll implements the pattern of querying at a fixed interval until the state changes.
//
// This skeleton is a collection of easy mistakes: applying the time limit to the in-flight
// query too, firing the first query without waiting for the interval, telling the caller's
// cancellation apart from our own timeout, and counting transient failures and resetting the
// count on success. When the same waiting pattern was once written in two places, only one of
// them had actually been fixed (in the form of the time limit not applying to the in-flight query).
//
// It holds only the way of waiting; what to wait for is passed in by the caller as a function.
// The only caller is veo.Client.Wait. It is internal because presenting it as general-purpose
// polling would attract demands for retry policies and backoff, fattening the skeleton.
package poll

import (
	"context"
	"fmt"
	"time"
)

// Loop is the configuration for one wait.
//
// Interval and Timeout must be positive. Resolving defaults is the caller's
// responsibility (veo fills them in New).
type Loop struct {
	// Interval is the gap until the next query.
	Interval time.Duration
	// Timeout is the time limit for the whole wait. The same deadline is passed to each
	// in-flight query, so a single query that never responds cannot keep the wait going.
	Timeout time.Duration
	// MaxErrors is how many transient failures are tolerated. Zero or below is treated as 1 (none tolerated).
	MaxErrors int
	// Subject is the subject of the error message (e.g. `file "files/abc"`).
	Subject string
	// RepeatedFailure is the sentinel wrapped when giving up after consecutive failures.
	// If nil, nothing is wrapped (for callers that have no classification sentinel).
	RepeatedFailure error
	// OnTransientError is called for each tolerated failure. It is a hook for logging;
	// if nil, nothing happens. It cannot stop the wait.
	OnTransientError func(err error, consecutive int)
}

// Run queries every Interval until fn reports completion.
//
// The first query is made immediately without waiting for the interval. On the path that
// resumes waiting from a different execution, the target has often already completed, and
// waiting one interval there is pure dead time.
//
// fn is one query and returns (result, completed, error). If completed is true, it is returned
// as is, error included (terminal states that waiting will not change, such as a server-side
// processing failure, arrive here). If completed is false with an error, it counts as a
// transient failure, the count resets on success, and the wait gives up after MaxErrors.
func (l Loop) Run[T any](ctx context.Context, fn func(context.Context) (T, bool, error)) (T, error) {
	var zero T

	waitCtx, cancel := context.WithTimeout(ctx, l.Timeout)
	defer cancel()

	ticker := time.NewTicker(l.Interval)
	defer ticker.Stop()

	maxErrors := max(l.MaxErrors, 1)
	consecutive := 0
	for {
		value, done, err := fn(waitCtx)
		switch {
		case done:
			return value, err

		case err != nil && waitCtx.Err() != nil:
			// A failure caused by reaching the wait's time limit is not a "transient failure".
			return zero, l.deadlineError(ctx, waitCtx.Err())

		case err != nil:
			consecutive++
			if consecutive >= maxErrors {
				return zero, l.repeatedFailureError(consecutive, err)
			}
			if l.OnTransientError != nil {
				l.OnTransientError(err, consecutive)
			}

		default:
			consecutive = 0
		}

		select {
		case <-waitCtx.Done():
			return zero, l.deadlineError(ctx, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

// deadlineError turns the reason the wait was abandoned into an error.
// The caller's context ending and this wait's time limit being reached call for different
// responses (the former is an upstream cancellation, the latter means the target's processing
// takes too long), so they can be told apart both by wording and by what is wrapped.
func (l Loop) deadlineError(ctx context.Context, waitErr error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("waiting for %s was interrupted: %w", l.Subject, ctx.Err())
	}
	return fmt.Errorf("%s did not complete within the time limit (%v): %w", l.Subject, l.Timeout, waitErr)
}

// repeatedFailureError turns the reason for giving up after repeated check failures into an error.
// The last cause can be reached through %w. The caller needs to look at the cause so as not to
// mistake a permanent failure (such as missing permissions) for a transient one.
func (l Loop) repeatedFailureError(consecutive int, cause error) error {
	if l.RepeatedFailure != nil {
		return fmt.Errorf("%w: checking %s failed %d times in a row: %w",
			l.RepeatedFailure, l.Subject, consecutive, cause)
	}
	return fmt.Errorf("checking %s failed %d times in a row: %w", l.Subject, consecutive, cause)
}
