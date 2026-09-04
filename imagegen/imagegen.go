// Package imagegen は、Vertex AI の画像モデルによる画像生成を実行します。
//
// 参照画像は gs:// URI だけを扱います。Vertex AI は gs:// をモデル側で解決できる
// ため、取得もアップロードもバイト列の転送も起きず、キットが持つのはプロンプトの
// 組み立て・シードの採番・レスポンスからの画像抽出だけです。参照画像を取得して
// インラインで送る経路や Gemini File API を経由する経路が必要な場合は、それらを
// 備えた gemini-image-kit を使ってください。
//
// 発射間隔・上限時間・重複排除といった呼び出しガードは持ちません。クォータは
// プロジェクト単位で操作の種類ごとではないため、画像生成だけを絞ってもテキスト生成が
// 同じクォータを食い尽くせてしまうからです。ガードは Generator を callguard で
// デコレートし、テキスト生成と 1 つの Guard を共有する形でワークフロー層に置いて
// ください。
package imagegen

import (
	"context"

	"github.com/shouni/genai-kit/gemini"
)

// Generator は、利用側が依存する画像生成の窓口です。
//
// 意図的に 1 メソッドです。利用側のテスト用フェイクがこれ 1 つで書けるようにする
// ためで、gemini.Generator が Generate だけを持つのと同じ理由です。
type Generator interface {
	// Generate は、参照画像（0〜複数）と構成パラメータに基づいて 1 枚の画像を生成します。
	Generate(ctx context.Context, req Request) (*Response, error)
}

// Client がパッケージ公開インターフェースを満たすことをコンパイル時に保証します。
var _ Generator = (*Client)(nil)

// Client は画像生成の実装です。
//
// 発射間隔・1 回あたりの上限時間・重複排除は持ちません。クォータはプロジェクト単位で
// 操作の種類ごとではないため、ライブラリごとに独立したレート制限を持たせると合計が
// クォータを超えます。ガードが要る場合は Client（Generator）を callguard で
// デコレートし、テキスト生成と 1 つの Guard を共有する形でワークフロー層に置いて
// ください。
type Client struct {
	ai       gemini.Generator
	autoSeed bool
}

// Option は Client の任意設定です。
type Option func(*Client)

// WithoutAutoSeed は、シード未指定のリクエストへの自動採番を無効にします。
//
// 既定では、Seed が未指定の生成にはランダムなシードを採番してから送信します。
// API 側にシード選択を委ねるとその値はレスポンスに含まれず、Response.UsedSeed が
// 0 のまま記録されて同条件での再生成ができなくなるためです（0 は有効なシードなので、
// 「未記録」と区別も付きません）。自動採番しても生成結果のランダム性は変わりません
// — シードを選ぶのが API 側か生成側かの違いです。
//
// このオプションは、シード管理を完全に呼び出し側で行う場合にのみ使ってください。
func WithoutAutoSeed() Option {
	return func(c *Client) {
		c.autoSeed = false
	}
}

// New は Client を作成します。
//
// ai は gemini.Generator（Generate の 1 メソッド）で足ります。
// テスト用フェイクもその 1 メソッドで書けます。
//
//	client, err := gemini.New(ctx, gemini.Config{ProjectID: "p", LocationID: "asia-northeast1"})
//	images, err := imagegen.New(client)
func New(ai gemini.Generator, opts ...Option) (*Client, error) {
	if ai == nil {
		return nil, ErrGeneratorRequired
	}

	c := &Client{ai: ai, autoSeed: true}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c, nil
}

// Generate は、参照画像（0〜複数）と構成パラメータに基づいて 1 枚の画像を生成します。
// 打ち切りは呼び出し側の context にのみ従います。
func (c *Client) Generate(ctx context.Context, req Request) (*Response, error) {
	prepared, err := c.prepare(req)
	if err != nil {
		return nil, err
	}

	attachments, err := attachmentsFor(req.Images)
	if err != nil {
		return nil, err
	}

	resp, err := c.ai.Generate(ctx, prepared.model, prepared.prompt, attachments, prepared.opts)
	if err != nil {
		return nil, err
	}
	return prepared.toResponse(resp)
}
