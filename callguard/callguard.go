// Package callguard は、外部 AI API の呼び出しに「発射間隔」「1 回あたりの上限時間」
// 「同一内容の同時実行の重複排除」をまとめて掛けるためのプリミティブです。
//
// クォータはプロジェクト単位で、操作の種類ごとではありません。テキスト生成と
// 画像生成で別々に絞っても意味がないため、ワークフロー全体で 1 つの Guard を共有し、
// 重複排除の単位（Group）だけを呼び出しの種類ごとに分けるのが想定した使い方です。
//
// 型付きのデコレータ（imagegen.Generator を包む、など）はここには置きません。
// 何を包みたいかは利用側の都合で、ここに用意すると口の数だけ増え続けます。
// デコレータは呼び出し側が持ち、その中で Do を呼んでください。
package callguard

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"strconv"
	"time"

	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
)

// DefaultExecTimeout は、共有実行 1 回あたりの上限時間の既定値です。
const DefaultExecTimeout = 5 * time.Minute

// Group は、同じキーの呼び出しをまとめる単位です。ゼロ値で使えます。
//
// Guard に持たせていないのは、発射間隔（プロジェクト単位のクォータ）と
// 重複排除の単位（呼び出しの内容）が別の粒度だからです。
type Group = singleflight.Group

// Guard は、発射間隔と 1 回あたりの上限時間を保持します。
//
// nil は「制限なし・既定の上限時間」として扱えます。呼び出し側に分岐を
// 持たせずそのまま渡せるようにするためで、テストが構造体リテラルで組む場合にも効きます。
type Guard struct {
	limiter *rate.Limiter
	timeout time.Duration
}

// Option は Guard の任意設定です。
type Option func(*Guard)

// WithRateInterval は AI 呼び出しの発射間隔の下限を設定します。
// 0 以下（既定）なら発射間隔の制限を行いません。
//
// スループットの上限はここで決まります。並列度をいくつに上げても 1/interval が
// 頭打ちで、発射間隔と並列度の両方を大きくする設定は矛盾しています。
func WithRateInterval(interval time.Duration) Option {
	return func(g *Guard) {
		if interval <= 0 {
			g.limiter = nil
			return
		}
		// バースト 1 は「interval ごとに 1 回」そのものです。まとめて撃てる枠を
		// 設けると、クォータ保護としての発射間隔の意味が消えます。
		g.limiter = rate.NewLimiter(rate.Every(interval), 1)
	}
}

// WithExecTimeout は共有実行 1 回あたりの上限時間を設定します。
// 0 以下（既定）なら DefaultExecTimeout を使います。
//
// 「無制限」は提供しません。共有実行は呼び出し元の context から切り離されるため、
// これが唯一の打ち切り手段です。無制限にすると、応答の返らない 1 回がゴルーチンを
// 抱えたまま残り、同じキーの後続を永久に待たせます。
func WithExecTimeout(d time.Duration) Option {
	return func(g *Guard) {
		g.timeout = d
	}
}

// New は Guard を組み立てます。
func New(opts ...Option) *Guard {
	g := &Guard{}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// execTimeout は適用する上限時間を返します。nil レシーバでも安全です。
func (g *Guard) execTimeout() time.Duration {
	if g == nil || g.timeout <= 0 {
		return DefaultExecTimeout
	}
	return g.timeout
}

// wait は次に呼び出してよい時刻まで待ちます。nil レシーバでも安全です。
func (g *Guard) wait(ctx context.Context) error {
	if g == nil || g.limiter == nil {
		return nil
	}
	return g.limiter.Wait(ctx)
}

// Do は同じ key の同時実行を 1 回にまとめ、呼び出し元のキャンセルも尊重します。
// guard が nil の場合は「発射間隔の制限なし・既定の上限時間」で実行します。
//
// 実行用 context を呼び出し元から切り離すのは、共有実行のクロージャの中です
// （context.WithoutCancel）。外で切り離すと、リーダー（最初の呼び出し元）が
// キャンセルして早期リターンした際に defer cancel() が共有実行を打ち切り、
// 相乗りしている他の呼び出し元が巻き添えになります。
//
// 発射間隔の待機は上限時間の外側で行います。待たされた時間を 1 回あたりの
// 上限時間に数えると、混雑しているだけでタイムアウトしてしまうためです。
//
// 戻り値は相乗りした全員で共有されます。呼び出し側が書き換える可能性があるものは
// 複製してから返してください。
func Do[T any](
	ctx context.Context,
	group *Group,
	guard *Guard,
	key string,
	fn func(execCtx context.Context) (T, error),
) (T, error) {
	ch := group.DoChan(key, func() (any, error) {
		baseCtx := context.WithoutCancel(ctx)

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
		// fn の静的な型付けにより result.Val は常に T なので、この分岐は実際には
		// 到達しません。将来のリファクタリングに対する防御として残しています。
		if !ok {
			var zero T
			return zero, fmt.Errorf("callguard: singleflight result type mismatch for key %s", key)
		}
		return value, nil
	}
}

// WriteHashPart は長さプレフィックス付きでハッシュへ部品を書き込みます。
//
// 長さを先に書くのは "ab"+"c" と "a"+"bc" のような連結の衝突を防ぐためです。
// キーを組み立てる関数はすべてこの 1 つの枠組みを共有してください。画像の
// バイト列のように文字列にすると複製が発生する部品は、Key ではなくこちらを直接使います。
func WriteHashPart(h hash.Hash, part []byte) {
	var lengthBuf [8]byte
	binary.LittleEndian.PutUint64(lengthBuf[:], uint64(len(part)))
	h.Write(lengthBuf[:])
	h.Write(part)
}

// Key は namespace と可変長の部品から衝突しにくい singleflight 用キーを作ります。
//
// 出力を変えるものは漏れなく部品に含めてください。特に seed は、プロンプトからは
// 導けないのに結果を変えるため、含め忘れると seed 違いの同時呼び出しが 1 回の生成結果を
// 共有します（SeedKey を使ってください）。
func Key(namespace string, parts ...string) string {
	hasher := sha256.New()
	for _, part := range parts {
		WriteHashPart(hasher, []byte(part))
	}
	return namespace + ":" + hex.EncodeToString(hasher.Sum(nil))
}

// SeedKey は nil と実値を区別できる seed 用のキー部品を作ります。
func SeedKey(seed *int64) string {
	if seed == nil {
		return "seed:nil"
	}
	return "seed:" + strconv.FormatInt(*seed, 10)
}
