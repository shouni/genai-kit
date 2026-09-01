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

// newLoop は、仮想時計向けの設定で Loop を作ります。値は実運用に近いものを使います
// （バブル内では実時間を消費しません）。
func newLoop(maxErrors int) Loop {
	return Loop{
		Interval:  10 * time.Second,
		Timeout:   5 * time.Minute,
		MaxErrors: maxErrors,
		Subject:   `対象 "x"`,
	}
}

// TestRunPollsImmediately は、1 回目の問い合わせを間隔待ちなしで行うことを検証します。
// 待ちを別実行から再開する経路では対象が既に完了していることが多く、そこで 1 interval
// 分待つのは純粋な死に時間になります。
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
			t.Errorf("elapsed = %v, want 0（間隔を待たずに 1 回目を撃つ）", elapsed)
		}
	})
}

// TestRunWaitsIntervalBetweenAttempts は、2 回目以降が間隔ちょうど空くことを
// 検証します。仮想時計なので、待たされた時間は設定値に一致します。
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

		// 1 回目は即時、2・3 回目が間隔を挟むので合計 2 間隔。
		if elapsed := time.Since(start); elapsed != 2*loop.Interval {
			t.Errorf("elapsed = %v, want %v", elapsed, 2*loop.Interval)
		}
	})
}

// TestRunReturnsTerminalError は、完了として返された失敗をそのまま返すことを
// 検証します。サーバー側の処理失敗のように待っても変わらない状態を、一時的な
// 失敗として数え直すと無駄に粘ることになります。
func TestRunReturnsTerminalError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sentinel := errors.New("処理に失敗しました")
		calls := 0

		_, err := newLoop(3).Run(context.Background(), func(context.Context) (int, bool, error) {
			calls++
			return 0, true, sentinel
		})

		if !errors.Is(err, sentinel) {
			t.Fatalf("Run() error = %v, want 終端の失敗", err)
		}
		if calls != 1 {
			t.Errorf("calls = %d, want 1（終端状態は再確認しない）", calls)
		}
	})
}

// TestRunToleratesTransientErrors は、一時的な失敗を受け流して待ち続け、成功したら
// 連続失敗のカウントが戻ることを検証します。カウントが戻らないと、長い待機の中で
// 散発する失敗が積算されて打ち切られます。
func TestRunToleratesTransientErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		results := []error{
			errors.New("blip 1"),
			errors.New("blip 2"),
			nil, // ここでカウントがリセットされる
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
			t.Errorf("連続失敗の回数 = %v, want %v（成功で数え直す）", observed, want)
		}
	})
}

// TestRunStopsAfterConsecutiveErrors は、確認が続けて失敗したら制限時間まで粘らずに
// 打ち切り、分類用センチネルと最後の原因の両方を辿れることを検証します。
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
			t.Fatalf("Run() error = %v, want RepeatedFailure センチネル", err)
		}
		if !errors.Is(err, cause) {
			t.Errorf("Run() error = %v, want 原因が辿れること", err)
		}
		if calls != 3 {
			t.Errorf("calls = %d, want 3（許容回数で打ち切る）", calls)
		}
	})
}

// TestRunStopsAfterConsecutiveErrorsWithoutSentinel は、分類用センチネルを持たない
// 呼び出し側でも、回数と原因が残ることを検証します。
func TestRunStopsAfterConsecutiveErrorsWithoutSentinel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cause := errors.New("network down")

		_, err := newLoop(2).Run(context.Background(), func(context.Context) (int, bool, error) {
			return 0, false, cause
		})

		if !errors.Is(err, cause) {
			t.Fatalf("Run() error = %v, want 原因が辿れること", err)
		}
		if !strings.Contains(err.Error(), "2 回連続で失敗") {
			t.Errorf("error = %q, want 回数を含むこと", err)
		}
	})
}

// TestRunMaxErrorsFloorsAtOne は、MaxErrors がゼロ以下でも 1 回目の失敗で
// 打ち切ることを検証します。0 をそのまま使うと打ち切り条件が成立しません。
func TestRunMaxErrorsFloorsAtOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0

		_, err := newLoop(0).Run(context.Background(), func(context.Context) (int, bool, error) {
			calls++
			return 0, false, errors.New("boom")
		})

		if err == nil {
			t.Fatal("Run() error = nil, want 打ち切り")
		}
		if calls != 1 {
			t.Errorf("calls = %d, want 1", calls)
		}
	})
}

// TestRunTimesOut は、完了しないまま制限時間に達した場合に打ち切ることを検証します。
func TestRunTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, err := newLoop(3).Run(context.Background(), func(context.Context) (int, bool, error) {
			return 0, false, nil
		})

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run() error = %v, want 期限切れ", err)
		}
		if !strings.Contains(err.Error(), "制限時間") {
			t.Errorf("error = %q, want 制限時間に達したと言うこと", err)
		}
	})
}

// TestRunTimeoutBoundsInFlightCall は、応答を返さない問い合わせが制限時間で
// 打ち切られることを検証します。
//
// このパッケージが存在する理由がこれです。genai の既定 HTTP クライアントは
// タイムアウトを持たないため、制限時間を問い合わせの合間でしか見張らない実装では、
// 1 回の中で止まったまま時間切れの判定へ到達できません。
func TestRunTimeoutBoundsInFlightCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0

		_, err := newLoop(3).Run(context.Background(), func(pollCtx context.Context) (int, bool, error) {
			calls++
			<-pollCtx.Done()
			return 0, false, pollCtx.Err()
		})

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run() error = %v, want 期限切れ", err)
		}
		if calls != 1 {
			t.Errorf("calls = %d, want 1（問い合わせの中で打ち切られる）", calls)
		}
	})
}

// TestRunStopsWhenCallerCancels は、呼び出し元のキャンセルを自前の時間切れと
// 区別することを検証します。前者は上位の判断、後者は対象の処理が長すぎるという
// 別の事象で、呼び出し側の対処が変わります。
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
		if strings.Contains(err.Error(), "制限時間") {
			t.Errorf("error = %q, want キャンセルを時間切れとして報告しないこと", err)
		}
	})
}
