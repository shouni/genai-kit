package veo

import (
	"log/slog"
	"time"
)

// Option は Client の設定を適用する関数型です。
// 不正な値（ゼロ以下）は「指定なし」として無視し、既定値のままにします。
type Option func(*Client)

// WithPollInterval は、生成完了を確認する間隔を設定します。
func WithPollInterval(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.pollInterval = d
		}
	}
}

// WithPollTimeout は、生成完了を待つ上限時間を設定します。
// 呼び出し側の context にこれより短い期限がある場合は、そちらが先に効きます。
func WithPollTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.pollTimeout = d
		}
	}
}

// WithMaxPollErrors は、生成状況の確認が連続して失敗するのを何回まで許容するかを
// 設定します。この回数に達した時点で ErrPollFailed として打ち切ります。
// 途中で1回でも成功すれば、カウントはゼロに戻ります。
func WithMaxPollErrors(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.maxPollErrors = n
		}
	}
}

// WithLogger は、このクライアントが出すログの出力先を設定します。
// 未指定（または nil）の場合は slog.Default() を使います。ジョブ ID などの属性を
// 付けたロガーを渡すと、ポーリングの警告ログにもその属性が乗ります。
func WithLogger(logger *slog.Logger) Option {
	return func(c *Client) {
		if logger != nil {
			c.logger = logger
		}
	}
}
