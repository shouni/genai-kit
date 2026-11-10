package poll

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// newLoop builds a Loop with settings for the virtual clock. The values are close to production
// ones (they consume no real time inside the bubble).
func newLoop(maxErrors int) Loop {
	return Loop{
		Interval:  10 * time.Second,
		Timeout:   5 * time.Minute,
		MaxErrors: maxErrors,
		Subject:   `target "x"`,
	}
}

// TestRunPollsImmediately checks that the first query is made without waiting for the interval.
// On the path that resumes waiting from a different execution, the target has often already
// completed, and waiting one interval there is pure dead time.
func TestRunPollsImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()

		got, err := newLoop(3).Run(context.Background(), func(context.Context) (string, bool, error) {
			return "done", true, nil
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if got != "done" {
			t.Errorf("value = %q, want done", got)
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Errorf("elapsed = %v, want 0 (fire the first query without waiting for the interval)", elapsed)
		}
	})
}

// TestRunWaitsIntervalBetweenAttempts checks that the second and later attempts are spaced exactly
// one interval apart. With the virtual clock, the time waited matches the configured value.
func TestRunWaitsIntervalBetweenAttempts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		loop := newLoop(3)
		calls := 0
		start := time.Now()

		if _, err := loop.Run(context.Background(), func(context.Context) (int, bool, error) {
			calls++
			return calls, calls == 3, nil
		}); err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		// The first is immediate and the second and third each wait an interval, so two intervals in total.
		if elapsed := time.Since(start); elapsed != 2*loop.Interval {
			t.Errorf("elapsed = %v, want %v", elapsed, 2*loop.Interval)
		}
	})
}

// TestRunReturnsTerminalError checks that a failure returned as completed is returned as is.
// Counting a state that waiting will not change, such as a server-side processing failure, as a
// transient failure would mean holding on pointlessly.
func TestRunReturnsTerminalError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sentinel := errors.New("processing failed")
		calls := 0

		_, err := newLoop(3).Run(context.Background(), func(context.Context) (int, bool, error) {
			calls++
			return 0, true, sentinel
		})

		if !errors.Is(err, sentinel) {
			t.Fatalf("Run() error = %v, want the terminal failure", err)
		}
		if calls != 1 {
			t.Errorf("calls = %d, want 1 (a terminal state is not checked again)", calls)
		}
	})
}

// TestRunToleratesTransientErrors checks that transient failures are tolerated and waiting
// continues, and that the consecutive-failure count resets on success. Without the reset,
// sporadic failures over a long wait would accumulate and cut it off.
func TestRunToleratesTransientErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		results := []error{
			errors.New("blip 1"),
			errors.New("blip 2"),
			nil, // the count resets here
			errors.New("blip 3"),
			errors.New("blip 4"),
			nil,
		}
		calls := 0
		var observed []int

		loop := newLoop(3)
		loop.OnTransientError = func(_ error, consecutive int) {
			observed = append(observed, consecutive)
		}

		got, err := loop.Run(context.Background(), func(context.Context) (string, bool, error) {
			i := calls
			calls++
			if i < len(results) {
				return "", false, results[i]
			}
			return "done", true, nil
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if got != "done" {
			t.Errorf("value = %q, want done", got)
		}
		if want := []int{1, 2, 1, 2}; !slices.Equal(observed, want) {
			t.Errorf("consecutive failure counts = %v, want %v (reset on success)", observed, want)
		}
	})
}

// TestRunStopsAfterConsecutiveErrors checks that repeated check failures give up without
// holding on until the time limit, and that both the classification sentinel and the last cause can be reached.
func TestRunStopsAfterConsecutiveErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sentinel := errors.New("poll: failed")
		cause := errors.New("permission denied")
		calls := 0

		loop := newLoop(3)
		loop.RepeatedFailure = sentinel

		_, err := loop.Run(context.Background(), func(context.Context) (int, bool, error) {
			calls++
			return 0, false, cause
		})

		if !errors.Is(err, sentinel) {
			t.Fatalf("Run() error = %v, want the RepeatedFailure sentinel", err)
		}
		if !errors.Is(err, cause) {
			t.Errorf("Run() error = %v, want the cause to be reachable", err)
		}
		if calls != 3 {
			t.Errorf("calls = %d, want 3 (give up at the tolerated count)", calls)
		}
	})
}

// TestRunStopsAfterConsecutiveErrorsWithoutSentinel checks that the count and the cause remain
// even for callers that have no classification sentinel.
func TestRunStopsAfterConsecutiveErrorsWithoutSentinel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cause := errors.New("network down")

		_, err := newLoop(2).Run(context.Background(), func(context.Context) (int, bool, error) {
			return 0, false, cause
		})

		if !errors.Is(err, cause) {
			t.Fatalf("Run() error = %v, want the cause to be reachable", err)
		}
		if !strings.Contains(err.Error(), "failed 2 times in a row") {
			t.Errorf("error = %q, want it to include the count", err)
		}
	})
}

// TestRunMaxErrorsFloorsAtOne checks that even with MaxErrors at zero or below, the first
// failure gives up. Using 0 as is would mean the give-up condition is never met.
func TestRunMaxErrorsFloorsAtOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0

		_, err := newLoop(0).Run(context.Background(), func(context.Context) (int, bool, error) {
			calls++
			return 0, false, errors.New("boom")
		})

		if err == nil {
			t.Fatal("Run() error = nil, want it to give up")
		}
		if calls != 1 {
			t.Errorf("calls = %d, want 1", calls)
		}
	})
}

// TestRunTimesOut checks that the wait gives up when the time limit is reached without completion.
func TestRunTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, err := newLoop(3).Run(context.Background(), func(context.Context) (int, bool, error) {
			return 0, false, nil
		})

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run() error = %v, want a deadline exceeded", err)
		}
		if !strings.Contains(err.Error(), "time limit") {
			t.Errorf("error = %q, want it to say the time limit was reached", err)
		}
	})
}

// TestRunTimeoutBoundsInFlightCall checks that a query that never responds is cut off at the
// time limit.
//
// This is why the package exists. genai's default HTTP client has no timeout, so an
// implementation that only watches the time limit between queries can get stuck inside one
// query and never reach the timeout check.
func TestRunTimeoutBoundsInFlightCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0

		_, err := newLoop(3).Run(context.Background(), func(pollCtx context.Context) (int, bool, error) {
			calls++
			<-pollCtx.Done()
			return 0, false, pollCtx.Err()
		})

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run() error = %v, want a deadline exceeded", err)
		}
		if calls != 1 {
			t.Errorf("calls = %d, want 1 (cut off inside the query)", calls)
		}
	})
}

// TestRunStopsWhenCallerCancels checks that the caller's cancellation is told apart from our own
// timeout. The former is an upstream decision and the latter means the target's processing takes
// too long; they are different events that call for different responses from the caller.
func TestRunStopsWhenCallerCancels(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := newLoop(3).Run(ctx, func(context.Context) (int, bool, error) {
			return 0, false, nil
		})

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
		if strings.Contains(err.Error(), "time limit") {
			t.Errorf("error = %q, want a cancellation not reported as a timeout", err)
		}
	})
}
