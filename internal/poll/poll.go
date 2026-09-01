// Package poll は、状態が変わるまで一定間隔で問い合わせ続ける待ち方をまとめます。
//
// この骨格は間違えやすい点の集まりです。制限時間を実行中の 1 回にも掛けること、
// 1 回目を間隔待ちなしで撃つこと、呼び出し元のキャンセルと自前の時間切れを区別
// すること、一時的な失敗を数えて成功で数え直すこと。かつて同じ待ち方が 2 か所に
// 書かれていたときは、実際に片方だけが直っていました（制限時間が実行中の
// 問い合わせに掛からない、という形で）。
//
// 待ち方だけを持ち、何を待つかは呼び出し側が関数で渡します。呼び出し元は
// veo.Client.Wait だけです。internal なのは、外向けの汎用ポーリングを名乗ると
// リトライ方針やバックオフの要求が付いてきて骨格が太るためです。
package poll

import (
	"context"
	"fmt"
	"time"
)

// Loop は 1 回分の待機の設定です。
//
// Interval と Timeout は正の値である必要があります。既定値の解決は呼び出し側の
// 責任です（veo は New で埋めています）。
type Loop struct {
	// Interval は次の問い合わせまでの間隔です。
	Interval time.Duration
	// Timeout は待機全体の上限時間です。実行中の問い合わせ 1 回にも同じ期限が
	// 渡るため、応答の返らない 1 回で待ち続けることはありません。
	Timeout time.Duration
	// MaxErrors は一時的な失敗を何回まで受け流すかです。0 以下は 1（受け流さない）扱いです。
	MaxErrors int
	// Subject はエラー文の主語です（例: `ファイル "files/abc"`）。
	Subject string
	// RepeatedFailure は連続失敗で打ち切ったときに包むセンチネルです。
	// nil なら包みません（呼び出し側に分類用のセンチネルが無い場合）。
	RepeatedFailure error
	// OnTransientError は受け流した失敗ごとに呼ばれます。ログのための口で、
	// nil なら何もしません。ここで待機を止めることはできません。
	OnTransientError func(err error, consecutive int)
}

// Run は fn が完了を返すまで、Interval ごとに問い合わせます。
//
// 1 回目は間隔を待たずに直ちに行います。別実行から待ちを再開する経路では対象が
// 既に完了していることが多く、そこで 1 interval 分待つのは純粋な死に時間です。
//
// fn は問い合わせ 1 回で、(結果, 完了したか, エラー) を返します。完了が true なら
// エラーごとそのまま返します（サーバー側の処理失敗のように、待っても変わらない終端
// 状態がここに来ます）。完了が false でエラーなら一時的な失敗として数え、成功で
// 数え直し、MaxErrors 回で打ち切ります。
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
			// 待ち時間の上限に達したことによる失敗は「一時的な失敗」ではない。
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

// deadlineError は、待機を打ち切った理由をエラーに整えます。
// 呼び出し元の context が終了した場合と、この待機の制限時間に達した場合とでは
// 対処が異なる（前者は上位のキャンセル、後者は対象の処理が長すぎる）ため、
// 文言でもラップ先でも区別できるようにしています。
func (l Loop) deadlineError(ctx context.Context, waitErr error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%s の待機が中断されました: %w", l.Subject, ctx.Err())
	}
	return fmt.Errorf("%s の処理が制限時間（%v）内に完了しませんでした: %w", l.Subject, l.Timeout, waitErr)
}

// repeatedFailureError は、確認が続けて失敗して打ち切った理由をエラーに整えます。
// 最後の原因は %w で辿れるようにします。恒久的な失敗（権限不足など）を一時的な
// 失敗と取り違えないために、呼び出し側が原因を見る必要があるためです。
func (l Loop) repeatedFailureError(consecutive int, cause error) error {
	if l.RepeatedFailure != nil {
		return fmt.Errorf("%w: %s の確認が %d 回連続で失敗しました: %w",
			l.RepeatedFailure, l.Subject, consecutive, cause)
	}
	return fmt.Errorf("%s の確認が %d 回連続で失敗しました: %w", l.Subject, consecutive, cause)
}
