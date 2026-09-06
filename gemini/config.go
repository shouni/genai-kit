package gemini

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"google.golang.org/genai"
)

var (
	// ErrConfigRequired は、ProjectID/LocationID と APIKey のいずれも設定されていない場合に返されます。
	ErrConfigRequired = errors.New("gemini: either ProjectID/LocationID or APIKey is required")
	// ErrExclusiveConfig は、ProjectID/LocationID と APIKey が同時に設定された場合に返されます。
	ErrExclusiveConfig = errors.New("gemini: ProjectID/LocationID and APIKey are mutually exclusive")
	// ErrIncompleteVertexConfig は、ProjectID と LocationID の一方のみが設定された場合に返されます。
	ErrIncompleteVertexConfig = errors.New("gemini: Vertex AI requires both ProjectID and LocationID")
)

const (
	// DefaultMaxRetries は、リトライ回数が未設定の場合に使用されるデフォルト値です。
	DefaultMaxRetries uint = 1
	// DefaultInitialDelay は、初期リトライ間隔が未設定の場合に使用されるデフォルト値です。
	DefaultInitialDelay time.Duration = 30 * time.Second
	// DefaultMaxDelay は、最大リトライ間隔が未設定の場合に使用されるデフォルト値です。
	DefaultMaxDelay time.Duration = 120 * time.Second
)

// Config は初期化用の設定です。
//
// 通常は Vertex AI を使います。ProjectID と LocationID を指定し、認証は
// Application Default Credentials (ADC) に従います。APIKey はそれが選べない
// モデルのための暫定的な例外です。
type Config struct {
	ProjectID  string // Google Cloud Project ID
	LocationID string // Location (e.g., "us-central1")

	// APIKey は Gemini API (Google AI Studio) バックエンドを使う場合のキーです。
	// ProjectID/LocationID とは排他で、指定するとバックエンドが切り替わります。
	//
	// 暫定的な例外です。このライブラリのバックエンドは Vertex AI に寄せてあり、
	// 呼び出し側がどちらを使っているか意識せずに済むことに価値があります。それでも
	// 残しているのは、最新の Lyria が Vertex AI では提供されておらず、音楽生成だけが
	// API キー経路でしか動かないためです。Vertex AI で使えるようになった時点で、
	// このフィールドと validate / toClientConfig の分岐ごと削除してください。
	APIKey string

	// MaxRetries は、1 回の呼び出しで許すリトライの回数です（初回実行は含みません）。
	// 0 は未設定で、DefaultMaxRetries を使います。リトライを止めたい場合は
	// DisableRetry を立ててください。
	MaxRetries uint

	// DisableRetry はリトライを完全に無効にし、1 回だけ実行します。
	// 成功のたびに副作用が生まれる呼び出しで、応答を取りこぼした際の再送が
	// 二重実行になる場合に使います。
	DisableRetry bool

	InitialDelay time.Duration
	MaxDelay     time.Duration

	// RequestTimeout は、生成呼び出し1回（リトライを含む）の上限時間です。
	// 0 は無制限で、呼び出し側の context の期限にのみ従います。
	// 動画生成の完了待ちには適用されません（veo 側の設定が受け持ちます）。
	RequestTimeout time.Duration

	// HTTPClient は genai SDK が使用する HTTP クライアントを差し替えます。
	// nil の場合は SDK のデフォルトが使われます。
	//
	// タイムアウトやプロキシを制御したい場合、あるいは SSRF 対策済みの
	// クライアントを使いたい場合に指定します。
	//
	//	cfg.HTTPClient = securenet.NewSafeHTTPClient(60 * time.Second)
	//
	// 認証は気にしなくて構いません。genai は HTTPClient を渡されると ADC の検出を
	// スキップして認証ヘッダ無しで送ってしまいますが、toClientConfig が認証情報を
	// 付け直します。渡したインスタンス自体は書き換えず、複製を使います。
	HTTPClient *http.Client
}

// validate は設定内容が正しいかをチェックします。
//
// 未設定（両方空）と書きかけ（片方だけ）を分けているのは、前者が「設定を渡し忘れた」、
// 後者が「片方の環境変数が空だった」という別々の間違いだからです。
//
// APIKey と Vertex AI の設定が同時に来た場合はどちらを使うか決められないため、
// 黙って一方を選ばずにエラーにします。
func (c Config) validate() error {
	hasVertexField := c.ProjectID != "" || c.LocationID != ""

	if hasVertexField && c.APIKey != "" {
		return ErrExclusiveConfig
	}
	if !hasVertexField {
		if c.APIKey != "" {
			return nil
		}
		return ErrConfigRequired
	}
	if c.ProjectID == "" || c.LocationID == "" {
		return ErrIncompleteVertexConfig
	}
	return nil
}

// usesAPIKey は、Gemini API バックエンドを使う設定かを返します。
// validate 済みの Config では、APIKey が入っていることがそのまま条件になります。
func (c Config) usesAPIKey() bool {
	return c.APIKey != ""
}

// toClientConfig Config を genai.ClientConfig に変換します。
//
// HTTPClient が指定されている場合は認証情報の付与も行います。genai は
// ClientConfig.HTTPClient が非 nil だと ADC の検出そのものをスキップし、渡された
// クライアントを認証ヘッダ無しで使うため、これが無いと全リクエストが 401
// （CREDENTIALS_MISSING）になります。つまり素の &http.Client{Timeout: ...} を渡すと、
// タイムアウトを設定したつもりで認証を捨てることになります。
func (c Config) toClientConfig() (*genai.ClientConfig, error) {
	cc := &genai.ClientConfig{}
	cc.HTTPOptions.RetryOptions = c.retryOptions()
	if c.usesAPIKey() {
		cc.APIKey = c.APIKey
		cc.Backend = genai.BackendGeminiAPI
	} else {
		cc.Project = c.ProjectID
		cc.Location = c.LocationID
		cc.Backend = genai.BackendVertexAI
	}

	if c.HTTPClient == nil {
		return cc, nil
	}
	// UseDefaultCredentials は渡されたクライアントの Transport を書き換えるため、
	// 呼び出し側が持っているインスタンスには触らないよう浅いコピーへ差し替える。
	// Timeout などの設定は引き継がれる。
	clone := *c.HTTPClient
	cc.HTTPClient = &clone

	// Gemini API の認証は API キーのヘッダ付与で、Transport には依存しない。
	if c.usesAPIKey() {
		return cc, nil
	}
	if err := cc.UseDefaultCredentials(); err != nil {
		return nil, fmt.Errorf("gemini: 指定された HTTPClient への認証情報の付与に失敗しました: %w", err)
	}
	return cc, nil
}

// orDefault は v が正の値であればそれを、そうでなければ def を返します。
func orDefault(v, def time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return def
}

// retryOptions は Config を genai SDK 内蔵リトライの設定へ変換します。
// nil を返すと SDK はリトライせず 1 回だけ実行します。
//
// 対象のステータス（408/429/5xx）と通信エラーの判定は SDK に任せます。
// 写し取ると、向こうが対象を増やしたときにこちらだけ古い一覧を持ち続けます。
func (c Config) retryOptions() *genai.HTTPRetryOptions {
	if c.DisableRetry {
		return nil
	}

	maxRetries := c.MaxRetries
	if maxRetries == 0 {
		maxRetries = DefaultMaxRetries
	}
	initialDelay := orDefault(c.InitialDelay, DefaultInitialDelay)
	maxDelay := orDefault(c.MaxDelay, DefaultMaxDelay)

	return &genai.HTTPRetryOptions{
		Attempts:     new(attemptsFrom(maxRetries)),
		InitialDelay: new(initialDelay.Seconds()),
		MaxDelay:     new(maxDelay.Seconds()),
		// SDK の既定は U(0, 1秒) の加算で、数十秒の間隔ではほぼ効きません。並列の
		// 生成が同じ 429 で弾かれたとき散らすのはジッタだけなので（callguard の
		// 発射間隔はリトライに掛からない）、間隔に比例した幅を持たせます。
		Jitter: new(initialDelay.Seconds() / 2),
	}
}

// noRetryHTTPOptions は、クライアントのリトライ設定をリクエスト単位で打ち消します。
// ポーリングのように呼び出し側が間隔とタイムアウトを持っている経路で使います。
// nil ではなく「1 回だけ」を渡すのは、genai がリクエスト側の RetryOptions を
// 非 nil のときだけ上書き適用するためです。
func noRetryHTTPOptions() *genai.HTTPOptions {
	return &genai.HTTPOptions{RetryOptions: &genai.HTTPRetryOptions{Attempts: new(int32(1))}}
}

// attemptsFrom はリトライ回数を genai の総試行回数（初回を含む）へ変換します。
// int32 に収まらない指定は飽和させます。
func attemptsFrom(maxRetries uint) int32 {
	if uint64(maxRetries) >= math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(maxRetries) + 1
}
