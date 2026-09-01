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

// TestDoCallerCancelDoesNotKillSharedExecution は、リーダー（最初の呼び出し元）が
// キャンセルしても共有実行が打ち切られず、相乗りしている呼び出し元が完走できることを
// 検証します。
//
// 実行用 context をクロージャの外で切り離すと、リーダーの defer cancel() が共有実行を
// 巻き添えにするため、このテストは失敗します。
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

		// リーダー A: 実行開始後にキャンセルする。
		ctxA, cancelA := context.WithCancel(context.Background())
		errA := make(chan error, 1)
		go func() {
			_, err := Do(ctxA, &group, nil, "same-key", fn)
			errA <- err
		}()
		<-started

		// 相乗り B: 同一キーで in-flight に合流し、完走を期待する。
		type result struct {
			val string
			err error
		}
		resB := make(chan result, 1)
		go func() {
			v, err := Do(context.Background(), &group, nil, "same-key", fn)
			resB <- result{v, err}
		}()

		synctest.Wait() // B が合流するまで待つ
		cancelA()
		require.ErrorIs(t, <-errA, context.Canceled)

		close(release)
		r := <-resB
		require.NoError(t, r.err, "リーダーのキャンセルで共有実行が巻き添えになっています")
		require.Equal(t, "ok", r.val)
	})
}

// TestDoDeduplicatesConcurrentCalls は、同一キーの同時呼び出しが 1 回にまとまり、
// 別キーはまとまらないことを検証します。
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
		assert.Equal(t, int32(2), calls.Load(), "同一キーは 1 回、別キーはもう 1 回")

		close(release)
		wg.Wait()
	})
}

// TestDoWaitsForRateIntervalOutsideExecTimeout は、発射間隔の待機が 1 回あたりの
// 上限時間に数えられないことを検証します。
//
// 数えてしまうと、混雑しているだけでタイムアウトします。上限時間より長く待たされた
// 2 本目が、実行そのものは一瞬でも失敗するようになります。
func TestDoWaitsForRateIntervalOutsideExecTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		const interval = 10 * time.Second
		// 上限時間を発射間隔よりずっと短くする。待機がこれに数えられるなら
		// 2 本目は DeadlineExceeded で落ちる。
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
		require.NoError(t, errs[1], "発射間隔の待機が上限時間に数えられています")

		// どちらか一方は 1 周期ぶん待たされている。
		assert.GreaterOrEqual(t, max(elapsed[0], elapsed[1]), interval, "発射間隔が効いていません")
	})
}

// TestDoAppliesExecTimeout は、共有実行が上限時間で打ち切られることを検証します。
// 共有実行は呼び出し元から切り離されるため、これが唯一の打ち切り手段です。
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

// TestDoPropagatesError は、共有実行の失敗が相乗りした全員へ届くことを検証します。
func TestDoPropagatesError(t *testing.T) {
	t.Parallel()

	sentinel := context.DeadlineExceeded
	var group Group

	_, err := Do(context.Background(), &group, nil, "key",
		func(context.Context) (int, error) { return 0, sentinel })

	require.ErrorIs(t, err, sentinel)
}

// TestNilGuardUsesDefaults は、nil の Guard が「制限なし・既定の上限時間」として
// 扱えることを検証します。呼び出し側に分岐を持たせないためです。
func TestNilGuardUsesDefaults(t *testing.T) {
	t.Parallel()

	var nilGuard *Guard
	assert.Equal(t, DefaultExecTimeout, nilGuard.execTimeout())
	assert.NoError(t, nilGuard.wait(context.Background()))

	assert.Equal(t, DefaultExecTimeout, New().execTimeout())
	assert.Equal(t, DefaultExecTimeout, New(WithExecTimeout(0)).execTimeout())
	assert.NoError(t, New(WithRateInterval(0)).wait(context.Background()))
}

// TestKeyIncludesSeed は、seed がキーに含まれることを検証します。
// 含まれないと、同一プロンプトで seed 違いの同時呼び出しが 1 回の生成結果を
// 共有してしまい、seed による出力の作り分けが効かなくなります。
func TestKeyIncludesSeed(t *testing.T) {
	t.Parallel()

	seedA := int64(1)
	seedB := int64(2)

	keyNil := Key("lyrics", "model", "prompt", SeedKey(nil))
	keyA := Key("lyrics", "model", "prompt", SeedKey(&seedA))
	keyB := Key("lyrics", "model", "prompt", SeedKey(&seedB))

	assert.NotEqual(t, keyA, keyB, "seed 違いで同じキーになっています")
	assert.NotEqual(t, keyA, keyNil, "seed 指定ありと nil で同じキーになっています")
	assert.NotEqual(t, keyB, keyNil, "seed 指定ありと nil で同じキーになっています")

	// 同一入力では安定していること（安定しないと重複排除が効かない）。
	assert.Equal(t, keyA, Key("lyrics", "model", "prompt", SeedKey(&seedA)))
}

// TestKeyPartsAreUnambiguous は、部品の境界が曖昧にならないことを検証します。
// 長さプレフィックスが無いと "ab"+"c" と "a"+"bc" が同じキーになり、
// 別の内容の生成が 1 回にまとめられます。
func TestKeyPartsAreUnambiguous(t *testing.T) {
	t.Parallel()

	assert.NotEqual(t, Key("ns", "ab", "c"), Key("ns", "a", "bc"))
	assert.NotEqual(t, Key("ns", "", "abc"), Key("ns", "abc", ""))
	assert.NotEqual(t, Key("a", "x"), Key("b", "x"), "namespace が違えば別キー")
	assert.True(t, len(Key("ns")) > len("ns:"), "namespace はキーの接頭辞として残る")
}

// TestWriteHashPartSharesKeyFraming は、WriteHashPart と Key が同じ枠組み
// （長さプレフィックス）を使っていることを検証します。枠組みが分かれると、
// 画像バイト列を直接ハッシュする経路だけが衝突しやすくなります。
func TestWriteHashPartSharesKeyFraming(t *testing.T) {
	t.Parallel()

	// Key と同じハッシュ関数を使って、枠組みだけを比べます。
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
		"長さプレフィックスが効いていません")
	assert.Equal(t,
		hashOf([]byte("ab"), []byte("c")),
		hashOf([]byte("ab"), []byte("c")))
}
