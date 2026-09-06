package lyria

import (
	"time"

	"github.com/shouni/genai-kit/gemini"
)

type options struct {
	geminiModel      string
	lyriaModel       string
	rateInterval     time.Duration
	textRateInterval time.Duration
	execTimeout      time.Duration
	audioGenerator   gemini.Generator
}

// Option は Workflow の構築時設定です。
type Option func(*options)

// WithGeminiModel は、歌詞生成とレシピ生成に使うモデルを指定します。
func WithGeminiModel(value string) Option {
	return func(opts *options) {
		opts.geminiModel = value
	}
}

// WithLyriaModel は、音声生成に使うモデルを指定します。
func WithLyriaModel(value string) Option {
	return func(opts *options) {
		opts.lyriaModel = value
	}
}

// WithAudioGenerator は、音声生成（Lyria）にだけ使う生成クライアントを差し替えます。
// 未指定なら New に渡した aiClient を作詞・作曲と共用します。
//
// genai SDK が Lyria の出力フォーマットを指定できるようになるまでの間、音声だけを
// REST 直叩きの実装へ逃がすための口です。差し替え点をここ（gemini.Generator）に置くのは、
// Workflow・Track・呼び出しガード・プロンプト構築をそのまま使い回し、戻すときは
// このオプションを外すだけにするためです。
func WithAudioGenerator(g gemini.Generator) Option {
	return func(opts *options) {
		opts.audioGenerator = g
	}
}

// WithRateInterval は、音声生成の発射間隔を指定します。未設定なら制限しません。
func WithRateInterval(value time.Duration) Option {
	return func(opts *options) {
		opts.rateInterval = value
	}
}

// WithTextRateInterval は、歌詞・レシピ生成（テキスト）の発射間隔を指定します。
// 未設定なら制限しません。音声側と別に持つのは、別のモデルの別のクォータだからです。
func WithTextRateInterval(value time.Duration) Option {
	return func(opts *options) {
		opts.textRateInterval = value
	}
}

// WithExecTimeout は、singleflight で共有される生成呼び出し 1 回あたりの上限時間です。
// 共有実行は呼び出し元の context から切り離されるため、これが唯一の打ち切り手段に
// なります。未設定なら callguard.DefaultExecTimeout です。
//
// 発射間隔の待機はこの時間に数えません（callguard.Do を参照）。
func WithExecTimeout(value time.Duration) Option {
	return func(opts *options) {
		opts.execTimeout = value
	}
}

func applyOptions(overrides ...Option) options {
	var opts options
	for _, override := range overrides {
		if override != nil {
			override(&opts)
		}
	}
	return opts
}

// jsonOptions は Gemini による JSON 形式の構造化データ生成に最適化されたオプションを返します。
// schema を指定すると構造化出力（constrained decoding）が有効になり、出力が文法レベルで制約されます。
func jsonOptions(seed *int64, schema *gemini.Schema) gemini.GenerateOptions {
	opts := baseOptions(seed, "application/json")
	opts.ResponseSchema = schema
	return opts
}

// audioOptions は Lyria による音声生成に最適化されたオプションを返します。
// Lyria モデルはレスポンス MIME type の指定なしで音声を返すため、指定しません。
func audioOptions(seed *int64) gemini.GenerateOptions {
	return baseOptions(seed, "")
}

// baseOptions はパッケージ共通の安全設定やシード値を適用したベースオプションを構築します。
// NOTE: 生成結果の再現性を優先するため、対応カテゴリのブロック閾値は BlockNone に統一しています。
// 入力・出力の制御は呼び出し側または後段処理で行う前提です。
func baseOptions(seed *int64, mimeType string) gemini.GenerateOptions {
	opts := gemini.GenerateOptions{
		SafetySettings: gemini.NewSafetySettings(gemini.SafetyBlockNone),
	}
	if seed != nil {
		opts.Seed = seed
	}
	if mimeType != "" {
		opts.ResponseMIMEType = mimeType
	}
	return opts
}
