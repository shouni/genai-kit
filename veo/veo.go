// Package veo は、Veo による動画生成を扱うクライアントを提供します。
//
// 動画生成は長時間実行オペレーションで、投函してから完了までポーリングし続ける必要が
// あります。gemini パッケージが持つのはその1往復ずつ（StartVideo / PollVideo）で、
// このパッケージが「どう待つか」——ポーリング間隔、タイムアウト、一時的な失敗を何回まで
// 許容するか——を受け持ちます。
//
// 依存は gemini.VideoGenerator の注入だけで、genai SDK には触れません。テストでは
// 2メソッドのフェイクを渡せば、GCP 認証も HTTP も無しでポーリング挙動を検証できます。
package veo

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/internal/poll"
)

const (
	// DefaultPollInterval は、生成完了を確認する既定の間隔です。
	// Veo の生成は分単位で掛かるため、短くしても API 呼び出しが増えるだけです。
	DefaultPollInterval = 10 * time.Second
	// DefaultPollTimeout は、完了を待つ既定の上限です。
	DefaultPollTimeout = 15 * time.Minute
	// DefaultMaxPollErrors は、ポーリングの連続失敗を許容する既定の回数です。
	// 一時的なエラーで生成済みの動画を取り逃がさない一方、恒久的な障害では
	// タイムアウトまで無駄に待たずに打ち切ります。
	DefaultMaxPollErrors = 10
)

// Request は動画生成1本分の入力です。
//
// gemini.VideoRequest の別名で、入力の組み立てにこのパッケージ独自の型を覚え直す
// 必要をなくしています。どちらの名前で書いても同じ型です。
type Request = gemini.VideoRequest

// Reference は referenceImages に渡す参照画像1枚です（gemini.VideoReference の別名）。
type Reference = gemini.VideoReference

// Media は動画生成に渡す画像・動画の入力です（gemini.Attachment の別名）。
// URI（Vertex AI では gs://）かバイト列のどちらかを設定します。
type Media = gemini.Attachment

// Result は完了した動画生成の結果です。
type Result struct {
	// OperationName は生成に使われたオペレーションの識別子です。
	// 課金や失敗の追跡でログに残せるよう保持しています。
	OperationName string
	// Videos は生成された動画です。Request.OutputGCSURI を指定した場合は URI が、
	// 指定しなかった場合は Data が入ります。
	Videos []gemini.Attachment
	// FilteredCount は安全性ポリシーで除外された本数です。1本も生成されなかった
	// 場合は Generate がエラーを返すため、ここが非ゼロで Videos も非空なのは
	// 複数本を要求して一部だけ除外されたケースです。
	FilteredCount int32
	// FilteredReasons は除外された理由です。
	FilteredReasons []string
}

// First は最初に生成された動画を返します。1本だけ要求する通常の使い方で、
// 添字アクセスと境界チェックを毎回書かずに済むようにしています。
// 動画が無い場合は false を返します。
func (r *Result) First() (gemini.Attachment, bool) {
	if r == nil || len(r.Videos) == 0 {
		return gemini.Attachment{}, false
	}
	return r.Videos[0], true
}

// Client は動画生成の投函から完了待ちまでを扱うクライアントです。
type Client struct {
	generator     gemini.VideoGenerator
	pollInterval  time.Duration
	pollTimeout   time.Duration
	maxPollErrors int
	logger        *slog.Logger
}

// New は、動画生成クライアントを注入して Client を初期化します。
//
// generator には *gemini.Client をそのまま渡せます。
//
//	gc, err := gemini.New(ctx, cfg)
//	vc, err := veo.New(gc, veo.WithPollInterval(10*time.Second))
func New(generator gemini.VideoGenerator, opts ...Option) (*Client, error) {
	if generator == nil {
		return nil, ErrGeneratorRequired
	}
	c := &Client{
		generator:     generator,
		pollInterval:  DefaultPollInterval,
		pollTimeout:   DefaultPollTimeout,
		maxPollErrors: DefaultMaxPollErrors,
		logger:        slog.Default(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Generate は動画生成を開始し、完了するまで待って結果を返します。
//
// 生成そのものが失敗した場合（安全性ポリシーによるブロックなど）は
// gemini.ErrVideoGenerationFailed を含むエラーになります。完了を待てなかった場合は
// ポーリングの打ち切り理由（タイムアウトまたは ErrPollFailed）を返します。
func (c *Client) Generate(ctx context.Context, model string, req Request) (*Result, error) {
	op, err := c.start(ctx, model, req)
	if err != nil {
		return nil, err
	}

	// 投函の応答が既に完了しているなら、1回分のポーリングを省いてそのまま返す。
	if op.Done {
		return resultFrom(op)
	}
	if strings.TrimSpace(op.Name) == "" {
		return nil, ErrMissingOperationName
	}
	return c.Wait(ctx, op.Name)
}

// Submit は動画生成を開始し、完了を待たずにオペレーション名を返します。
//
// Wait と対になる入口です。実行時間に上限のあるジョブ基盤で、投函だけ済ませて一旦
// 戻り、次の実行で名前を渡して待ちを再開する、といった使い方ができます。これが無いと
// 投函側だけ veo を通らず gemini.StartVideo を直接呼ぶことになり、投函と待ちで
// 依存先が割れます。
//
// 返した名前はそのまま Wait に渡せます。
func (c *Client) Submit(ctx context.Context, model string, req Request) (string, error) {
	op, err := c.start(ctx, model, req)
	if err != nil {
		return "", err
	}
	// 完了済みでも名前は返す。結果は Wait 側で 1 回のポーリングにより取得できる。
	if strings.TrimSpace(op.Name) == "" {
		return "", ErrMissingOperationName
	}
	return op.Name, nil
}

// start は動画生成を投函し、応答の欠落を弾いた上でオペレーションを返します。
func (c *Client) start(ctx context.Context, model string, req Request) (*gemini.VideoOperation, error) {
	op, err := c.generator.StartVideo(ctx, model, req)
	if err != nil {
		return nil, err
	}
	if op == nil {
		return nil, fmt.Errorf("veo: %w", gemini.ErrEmptyResponse)
	}
	c.logger.InfoContext(ctx, "動画生成オペレーションを開始しました", "operation", op.Name, "model", model)
	return op, nil
}

// Wait は、開始済みの動画生成オペレーションが完了するまでポーリングして結果を返します。
//
// Generate から分けているのは、投函と完了待ちを別のプロセス・別の実行で行える
// ようにするためです（実行時間に上限のあるジョブ基盤で、投函だけ済ませて一旦戻り、
// 次の実行で名前を渡して待ちを再開する、といった使い方ができます）。
//
// 待ち方は internal/poll が持ちます。最初の問い合わせは間隔を待たずに直ちに行い
// （再開ではオペレーションが既に完了していることが多く、1 interval 分＝既定 10 秒
// 待ってから確認するのは純粋な死に時間になるためです）、制限時間は実行中の 1 回にも
// 掛かります。
//
// 1回ごとの問い合わせにはリトライを掛けません。一時的な失敗は maxPollErrors 回まで
// 受け流し、それを超えた時点で ErrPollFailed として打ち切ります
// （gemini.PollVideo のコメント参照）。
func (c *Client) Wait(ctx context.Context, operationName string) (*Result, error) {
	if strings.TrimSpace(operationName) == "" {
		return nil, ErrMissingOperationName
	}

	loop := poll.Loop{
		Interval:        c.pollInterval,
		Timeout:         c.pollTimeout,
		MaxErrors:       c.maxPollErrors,
		Subject:         fmt.Sprintf("オペレーション %q", operationName),
		RepeatedFailure: ErrPollFailed,
		OnTransientError: func(err error, consecutive int) {
			c.logger.WarnContext(ctx, "動画生成の状況確認に失敗しました。再確認します",
				"operation", operationName, "consecutive_errors", consecutive, "error", err)
		},
	}

	op, err := loop.Run(ctx, func(pollCtx context.Context) (*gemini.VideoOperation, bool, error) {
		op, err := c.generator.PollVideo(pollCtx, operationName)
		if err != nil {
			return nil, false, err
		}
		return op, op.Done, nil
	})
	if err != nil {
		return nil, err
	}
	return resultFrom(op)
}

// resultFrom は、完了したオペレーションを結果へ変換します。
func resultFrom(op *gemini.VideoOperation) (*Result, error) {
	if op.Failure != nil {
		return nil, fmt.Errorf("オペレーション %q: %w", op.Name, op.Failure)
	}
	if len(op.Videos) == 0 {
		return nil, noVideoError(op)
	}
	return &Result{
		OperationName:   op.Name,
		Videos:          op.Videos,
		FilteredCount:   op.FilteredCount,
		FilteredReasons: op.FilteredReasons,
	}, nil
}

// noVideoError は、成功したのに動画が返らなかった場合のエラーを組み立てます。
// 安全性ポリシーによる除外がこの経路の大半を占めるため、理由が分かっている
// ときはメッセージに含めます（プロンプトを直すのに必要な情報です）。
func noVideoError(op *gemini.VideoOperation) error {
	if op.FilteredCount > 0 || len(op.FilteredReasons) > 0 {
		return fmt.Errorf("%w: 安全性ポリシーにより除外されました（件数=%d, 理由=%s）",
			ErrNoVideoGenerated, op.FilteredCount, strings.Join(op.FilteredReasons, "; "))
	}
	return fmt.Errorf("%w: オペレーション %q", ErrNoVideoGenerated, op.Name)
}
