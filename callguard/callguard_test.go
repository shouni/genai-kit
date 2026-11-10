package callguard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDoCallerCancelDoesNotKillSharedExecution checks that when the leader (the first caller)
// cancels, the shared execution is not cut off, and the callers sharing it can run to
// completion.
//
// If the execution context were detached outside the closure, the leader's defer cancel() would
// take the shared execution down with it, and this test fails.
func TestDoCallerCancelDoesNotKillSharedExecution(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var group Group
		release := make(chan struct{})
		started := make(chan struct{})
		var once sync.Once

		fn := func(execCtx context.Context) (string, error) {
			once.Do(func() { close(started) })
			select {
			case <-release:
				return "ok", nil
			case <-execCtx.Done():
				return "", execCtx.Err()
			}
		}

		// Leader A: cancels after execution starts.
		ctxA, cancelA := context.WithCancel(context.Background())
		errA := make(chan error, 1)
		go func() {
			_, err := Do(ctxA, &group, nil, "same-key", fn)
			errA <- err
		}()
		<-started

		// Joiner B: joins the in-flight call with the same key and is expected to complete.
		type result struct {
			val string
			err error
		}
		resB := make(chan result, 1)
		go func() {
			v, err := Do(context.Background(), &group, nil, "same-key", fn)
			resB <- result{v, err}
		}()

		synctest.Wait() // wait for B to join
		cancelA()
		require.ErrorIs(t, <-errA, context.Canceled)

		close(release)
		r := <-resB
		require.NoError(t, r.err, "the leader's cancellation took the shared execution down with it")
		require.Equal(t, "ok", r.val)
	})
}

// TestDoDeduplicatesConcurrentCalls checks that concurrent calls with the same key collapse into
// one, and that different keys do not.
func TestDoDeduplicatesConcurrentCalls(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var group Group
		var calls atomic.Int32
		release := make(chan struct{})

		fn := func(context.Context) (int, error) {
			calls.Add(1)
			<-release
			return 1, nil
		}

		var wg sync.WaitGroup
		for range 5 {
			wg.Go(func() { _, _ = Do(context.Background(), &group, nil, "same", fn) })
		}
		wg.Go(func() { _, _ = Do(context.Background(), &group, nil, "other", fn) })

		synctest.Wait()
		assert.Equal(t, int32(2), calls.Load(), "once for the same key, once more for the other key")

		close(release)
		wg.Wait()
	})
}

// TestDoWaitsForRateIntervalOutsideExecTimeout checks that waiting for the firing interval is
// not counted in the per-call time limit.
//
// If it were counted, calls would time out merely from congestion. A second call kept waiting
// longer than the time limit would fail even though the execution itself takes an instant.
func TestDoWaitsForRateIntervalOutsideExecTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		const interval = 10 * time.Second
		// Make the time limit much shorter than the firing interval. If the wait counted toward
		// it, the second call would fail with DeadlineExceeded.
		guard := New(WithRateInterval(interval), WithExecTimeout(time.Second))

		var group Group
		start := time.Now()
		var elapsed [2]time.Duration
		var errs [2]error

		var wg sync.WaitGroup
		for i := range 2 {
			wg.Go(func() {
				_, errs[i] = Do(context.Background(), &group, guard, "key-"+string(rune('a'+i)),
					func(context.Context) (int, error) { return i, nil })
				elapsed[i] = time.Since(start)
			})
		}
		wg.Wait()

		require.NoError(t, errs[0])
		require.NoError(t, errs[1], "the firing-interval wait is counted in the time limit")

		// One of the two was kept waiting for one period.
		assert.GreaterOrEqual(t, max(elapsed[0], elapsed[1]), interval, "the firing interval is not applied")
	})
}

// TestDoAppliesExecTimeout checks that a shared execution is cut off at the time limit.
// A shared execution is detached from its callers, so this is the only way to cut it off.
func TestDoAppliesExecTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		guard := New(WithExecTimeout(30 * time.Second))
		var group Group

		_, err := Do(context.Background(), &group, guard, "key",
			func(execCtx context.Context) (int, error) {
				<-execCtx.Done()
				return 0, execCtx.Err()
			})

		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

// TestDoPropagatesError checks that a failure of the shared execution reaches everyone sharing it.
func TestDoPropagatesError(t *testing.T) {
	t.Parallel()

	sentinel := context.DeadlineExceeded
	var group Group

	_, err := Do(context.Background(), &group, nil, "key",
		func(context.Context) (int, error) { return 0, sentinel })

	require.ErrorIs(t, err, sentinel)
}

// TestDoSkipsExecutionWhenContextAlreadyDone checks that fn is not run with a ctx that is
// already done. Execution is detached from the caller, so unless it stops here, a billed call
// runs with nobody to receive the result.
func TestDoSkipsExecutionWhenContextAlreadyDone(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		var group Group
		var calls atomic.Int32
		for range 3 {
			_, err := Do(ctx, &group, nil, "key", func(context.Context) (int, error) {
				calls.Add(1)
				return 1, nil
			})
			require.ErrorIs(t, err, context.Canceled)
		}

		synctest.Wait()
		require.Zero(t, calls.Load(), "fn ran with a cancelled ctx")
	})
}

// TestDoRecoversPanic checks that a panic in fn does not bring the process down and reaches
// everyone sharing the call as ErrPanicked.
func TestDoRecoversPanic(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var group Group
		release := make(chan struct{})
		errs := make(chan error, 2)

		for range 2 {
			go func() {
				_, err := Do(context.Background(), &group, nil, "key",
					func(context.Context) (int, error) {
						<-release
						panic("boom")
					})
				errs <- err
			}()
		}

		synctest.Wait() // wait for the second caller to join
		close(release)

		for range 2 {
			err := <-errs
			require.ErrorIs(t, err, ErrPanicked)
			require.ErrorContains(t, err, "boom")
		}
	})
}

// TestNilGuardUsesDefaults checks that a nil Guard can be treated as "no limits, default
// time limit". This keeps callers free of branches.
func TestNilGuardUsesDefaults(t *testing.T) {
	t.Parallel()

	var nilGuard *Guard
	assert.Equal(t, DefaultExecTimeout, nilGuard.execTimeout())
	assert.NoError(t, nilGuard.wait(context.Background()))

	assert.Equal(t, DefaultExecTimeout, New().execTimeout())
	assert.Equal(t, DefaultExecTimeout, New(WithExecTimeout(0)).execTimeout())
	assert.NoError(t, New(WithRateInterval(0)).wait(context.Background()))
}

// TestKeyIncludesSeed checks that the seed is part of the key.
// Otherwise, concurrent calls with the same prompt but different seeds would share the result
// of one generation, and producing different outputs per seed would stop working.
func TestKeyIncludesSeed(t *testing.T) {
	t.Parallel()

	seedA := int64(1)
	seedB := int64(2)

	keyNil := Key("lyrics", "model", "prompt", SeedKey(nil))
	keyA := Key("lyrics", "model", "prompt", SeedKey(&seedA))
	keyB := Key("lyrics", "model", "prompt", SeedKey(&seedB))

	assert.NotEqual(t, keyA, keyB, "different seeds produce the same key")
	assert.NotEqual(t, keyA, keyNil, "a specified seed and nil produce the same key")
	assert.NotEqual(t, keyB, keyNil, "a specified seed and nil produce the same key")

	// Stable for the same input (otherwise deduplication would not work).
	assert.Equal(t, keyA, Key("lyrics", "model", "prompt", SeedKey(&seedA)))
}

// TestKeyPartsAreUnambiguous checks that the boundaries between parts are unambiguous.
// Without a length prefix, "ab"+"c" and "a"+"bc" would give the same key, and generations
// with different content would be collapsed into one.
func TestKeyPartsAreUnambiguous(t *testing.T) {
	t.Parallel()

	assert.NotEqual(t, Key("ns", "ab", "c"), Key("ns", "a", "bc"))
	assert.NotEqual(t, Key("ns", "", "abc"), Key("ns", "abc", ""))
	assert.NotEqual(t, Key("a", "x"), Key("b", "x"), "a different namespace gives a different key")
	assert.True(t, len(Key("ns")) > len("ns:"), "the namespace remains as the key prefix")
}

// TestWriteHashPartSharesKeyFraming checks that WriteHashPart and Key use the same framing
// (a length prefix). If the framing diverged, only the path that hashes image bytes directly
// would become more prone to collisions.
func TestWriteHashPartSharesKeyFraming(t *testing.T) {
	t.Parallel()

	// Using the same hash function as Key, compare only the framing.
	hashOf := func(parts ...[]byte) string {
		h := sha256.New()
		for _, p := range parts {
			WriteHashPart(h, p)
		}
		return hex.EncodeToString(h.Sum(nil))
	}

	assert.NotEqual(t,
		hashOf([]byte("ab"), []byte("c")),
		hashOf([]byte("a"), []byte("bc")),
		"the length prefix is not applied")
	assert.Equal(t,
		hashOf([]byte("ab"), []byte("c")),
		hashOf([]byte("ab"), []byte("c")))
}
