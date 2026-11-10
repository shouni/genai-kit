// Package callguard provides primitives that apply a "firing interval", a "per-call time limit"
// and "deduplication of concurrent identical calls" to calls to external AI APIs.
//
// Quota is per project, not per kind of operation. Throttling text generation and image
// generation separately achieves nothing, so the intended use is to share one Guard across the
// whole workflow and split only the deduplication unit (Group) per kind of call.
//
// Typed decorators (wrapping imagegen.Generator, and so on) do not live here.
// What to wrap is up to the caller, and providing them here would keep adding one per interface.
// Callers own the decorators and call Do inside them.
package callguard

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"log/slog"
	"runtime/debug"
	"strconv"
	"time"

	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
)

// DefaultExecTimeout is the default time limit for one shared execution.
const DefaultExecTimeout = 5 * time.Minute

// ErrPanicked indicates that the fn of a shared execution panicked.
//
// fn runs in a separate goroutine started by singleflight, so the caller's recover does not reach
// it. Left alone, singleflight re-panics and the whole process goes down, taking other jobs sharing
// the same process with it. Do recovers and folds it into this error.
var ErrPanicked = errors.New("callguard: shared execution panicked")

// Group is the unit that collapses calls with the same key. Its zero value is ready to use.
//
// It is not held by Guard because the firing interval (a per-project quota) and the deduplication
// unit (the content of the call) have different granularity.
type Group = singleflight.Group

// Guard holds the firing interval and the per-call time limit.
//
// nil can be treated as "no limits, default time limit". This lets callers pass it along without
// branches, and also helps when tests build one with a struct literal.
type Guard struct {
	limiter *rate.Limiter
	timeout time.Duration
}

// Option is an optional Guard setting.
type Option func(*Guard)

// WithRateInterval sets the minimum firing interval between AI calls.
// At 0 or below (the default), the firing interval is not limited.
//
// This decides the throughput ceiling. However high the parallelism goes, 1/interval is the cap,
// so a setting that raises both the firing interval and the parallelism is self-contradictory.
func WithRateInterval(interval time.Duration) Option {
	return func(g *Guard) {
		if interval <= 0 {
			g.limiter = nil
			return
		}
		// A burst of 1 is exactly "once per interval". Allowing a slot that fires several at once
		// would remove the meaning of the firing interval as quota protection.
		g.limiter = rate.NewLimiter(rate.Every(interval), 1)
	}
}

// WithExecTimeout sets the time limit for one shared execution.
// At 0 or below (the default), DefaultExecTimeout is used.
//
// "Unlimited" is not offered. A shared execution is detached from the caller's context, so this
// is the only way to cut it off. Without a limit, a single call that never responds would remain
// holding its goroutine and keep later calls with the same key waiting forever.
func WithExecTimeout(d time.Duration) Option {
	return func(g *Guard) {
		g.timeout = d
	}
}

// New builds a Guard.
func New(opts ...Option) *Guard {
	g := &Guard{}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// execTimeout returns the time limit to apply. Safe on a nil receiver.
func (g *Guard) execTimeout() time.Duration {
	if g == nil || g.timeout <= 0 {
		return DefaultExecTimeout
	}
	return g.timeout
}

// wait waits until the next call is allowed. Safe on a nil receiver.
func (g *Guard) wait(ctx context.Context) error {
	if g == nil || g.limiter == nil {
		return nil
	}
	return g.limiter.Wait(ctx)
}

// Do collapses concurrent executions with the same key into one, while respecting the caller's cancellation.
// If guard is nil, it runs with "no firing interval limit, default time limit".
//
// The execution context is detached from the caller inside the shared execution's closure
// (context.WithoutCancel). Detaching it outside would let the leader's (the first caller's)
// defer cancel() cut off the shared execution when the leader cancels and returns early,
// taking the other callers sharing it down with it.
//
// Waiting for the firing interval happens outside the time limit. Counting the time spent
// waiting toward the per-call time limit would make calls time out merely from congestion.
//
// fn is not run with a ctx that is already done. Execution is detached from the caller, so
// without this check a billed call with nobody to receive the result would run to the end
// (after a job is cut off, the rest of a batch would keep firing one by one).
// The check happens only before joining; detachment after joining is unchanged.
//
// A panic in fn is recovered and folded into ErrPanicked, and the same error is returned to everyone sharing the call.
//
// The return value is shared by everyone who joined the call. Copy anything the caller might
// modify before returning it.
func Do[T any](
	ctx context.Context,
	group *Group,
	guard *Guard,
	key string,
	fn func(execCtx context.Context) (T, error),
) (T, error) {
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}

	ch := group.DoChan(key, func() (val any, err error) {
		baseCtx := context.WithoutCancel(ctx)

		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			slog.ErrorContext(baseCtx, "callguard: shared execution panicked",
				"key", key,
				"panic", recovered,
				"stack", string(debug.Stack()),
			)
			val, err = nil, fmt.Errorf("%w: %v", ErrPanicked, recovered)
		}()

		if err := guard.wait(baseCtx); err != nil {
			return nil, err
		}

		execCtx, cancel := context.WithTimeout(baseCtx, guard.execTimeout())
		defer cancel()
		return fn(execCtx)
	})

	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			var zero T
			return zero, result.Err
		}

		value, ok := result.Val.(T)
		// Given fn's static typing, result.Val is always T, so this branch is never actually
		// reached. It is kept as a defence against future refactoring.
		if !ok {
			var zero T
			return zero, fmt.Errorf("callguard: singleflight result type mismatch for key %s", key)
		}
		return value, nil
	}
}

// WriteHashPart writes a part to the hash with a length prefix.
//
// The length is written first to prevent concatenation collisions such as "ab"+"c" versus "a"+"bc".
// Every function building keys should share this one framing. Parts that would be copied if
// turned into strings, such as image bytes, use this directly instead of Key.
func WriteHashPart(h hash.Hash, part []byte) {
	var lengthBuf [8]byte
	binary.LittleEndian.PutUint64(lengthBuf[:], uint64(len(part)))
	h.Write(lengthBuf[:])
	h.Write(part)
}

// Key builds a collision-resistant singleflight key from a namespace and variadic parts.
//
// Include every part that changes the output. The seed in particular cannot be derived from the
// prompt yet changes the result, so leaving it out makes concurrent calls with different seeds
// share the result of one generation (use SeedKey).
func Key(namespace string, parts ...string) string {
	hasher := sha256.New()
	for _, part := range parts {
		WriteHashPart(hasher, []byte(part))
	}
	return namespace + ":" + hex.EncodeToString(hasher.Sum(nil))
}

// SeedKey builds a seed key part that distinguishes nil from an actual value.
func SeedKey(seed *int64) string {
	if seed == nil {
		return "seed:nil"
	}
	return "seed:" + strconv.FormatInt(*seed, 10)
}
