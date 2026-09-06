package gemini

import (
	"errors"
	"math"
	"net/http"
	"testing"
	"time"

	"cloud.google.com/go/auth/credentials"
	"google.golang.org/genai"
)

// skipWithoutGCPCredentials は、Application Default Credentials が利用できない環境
// （CI ランナーなど）でテストをスキップします。
//
// genai の UseDefaultCredentials は cloud-platform スコープで ADC を検出するため、
// 同じ検出器を同じスコープで呼んで前提条件だけを確かめます。
func skipWithoutGCPCredentials(t *testing.T) {
	t.Helper()

	_, err := credentials.DetectDefault(&credentials.DetectOptions{
		Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
	})
	if err != nil {
		t.Skipf("ADC が見つからないため、このテストをスキップします: %v", err)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want error
	}{
		{
			name: "正常系: ProjectID と LocationID が揃っている",
			cfg:  Config{ProjectID: "my-project", LocationID: "asia-northeast1"},
			want: nil,
		},
		{
			// 設定を渡し忘れた、という間違い。
			name: "異常系: どちらも空",
			cfg:  Config{},
			want: ErrConfigRequired,
		},
		{
			// 環境変数の片方が空だった、という別の間違い。
			name: "異常系: ProjectID のみ",
			cfg:  Config{ProjectID: "my-project"},
			want: ErrIncompleteVertexConfig,
		},
		{
			name: "異常系: LocationID のみ",
			cfg:  Config{LocationID: "asia-northeast1"},
			want: ErrIncompleteVertexConfig,
		},
		{
			name: "正常系: APIKey のみ",
			cfg:  Config{APIKey: "key"},
			want: nil,
		},
		{
			// どちらを使うか決められないため、黙って一方を選ばずに落とす。
			name: "異常系: APIKey と Vertex AI の併用",
			cfg:  Config{ProjectID: "my-project", LocationID: "asia-northeast1", APIKey: "key"},
			want: ErrExclusiveConfig,
		},
		{
			// 書きかけの Vertex 設定より、併用そのものを先に知らせる。
			name: "異常系: APIKey と書きかけの Vertex 設定",
			cfg:  Config{ProjectID: "my-project", APIKey: "key"},
			want: ErrExclusiveConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if tt.want == nil {
				if err != nil {
					t.Fatalf("validate() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Errorf("validate() error = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestConfigToClientConfig は、設定からクライアント設定が組み上がることを検証します。
// APIKey を指定した場合だけ Gemini API バックエンドへ切り替わります。
func TestConfigToClientConfig(t *testing.T) {
	t.Run("Vertex AI", func(t *testing.T) {
		got, err := Config{ProjectID: "proj-v", LocationID: "loc-v"}.toClientConfig()
		if err != nil {
			t.Fatalf("toClientConfig() error = %v", err)
		}

		if got.Project != "proj-v" || got.Location != "loc-v" {
			t.Errorf("project/location = %q/%q", got.Project, got.Location)
		}
		if got.Backend != genai.BackendVertexAI {
			t.Errorf("Backend = %v, want Vertex AI", got.Backend)
		}
		// HTTPClient を渡していないので、ADC の検出は SDK 側に委ねられる。
		if got.HTTPClient != nil {
			t.Errorf("HTTPClient = %+v, want nil (SDK の既定に委ねる)", got.HTTPClient)
		}
		if got.HTTPOptions.RetryOptions == nil {
			t.Error("RetryOptions がクライアント設定に載っていません")
		}
	})

	t.Run("Gemini API", func(t *testing.T) {
		got, err := Config{APIKey: "key"}.toClientConfig()
		if err != nil {
			t.Fatalf("toClientConfig() error = %v", err)
		}

		if got.APIKey != "key" {
			t.Errorf("APIKey = %q", got.APIKey)
		}
		if got.Backend != genai.BackendGeminiAPI {
			t.Errorf("Backend = %v, want Gemini API", got.Backend)
		}
		// Vertex AI の項目を巻き込まないこと。載せると SDK 側の判定を惑わせる。
		if got.Project != "" || got.Location != "" {
			t.Errorf("project/location = %q/%q, want いずれも空", got.Project, got.Location)
		}
		if got.HTTPOptions.RetryOptions == nil {
			t.Error("RetryOptions がクライアント設定に載っていません")
		}
	})
}

// TestToClientConfigKeepsSuppliedHTTPClientForAPIKey は、API キー経路で HTTPClient を
// 渡しても ADC の付与を試みないことを検証します。
//
// Gemini API の認証はキーのヘッダ付与で、Transport には依存しません。ここで
// UseDefaultCredentials を呼ぶと、ADC の無い環境（API キーだけを持つ実行環境が
// まさにそれです）でクライアントの生成そのものが失敗します。
func TestToClientConfigKeepsSuppliedHTTPClientForAPIKey(t *testing.T) {
	supplied := &http.Client{Timeout: 42 * time.Second}

	got, err := Config{APIKey: "key", HTTPClient: supplied}.toClientConfig()
	if err != nil {
		t.Fatalf("toClientConfig() error = %v", err)
	}

	if got.HTTPClient == nil {
		t.Fatal("HTTPClient がクライアント設定に載っていません")
	}
	if got.HTTPClient == supplied {
		t.Error("呼び出し側のインスタンスがそのまま使われています（複製すること）")
	}
	if got.HTTPClient.Timeout != supplied.Timeout {
		t.Errorf("Timeout = %v, want %v", got.HTTPClient.Timeout, supplied.Timeout)
	}
	if got.HTTPClient.Transport != nil {
		t.Errorf("Transport = %+v, want nil (API キー経路では認証情報を付けない)", got.HTTPClient.Transport)
	}
}

func TestConfigRetryOptions(t *testing.T) {
	t.Run("既定値が適用されること", func(t *testing.T) {
		got := Config{}.retryOptions()
		if got == nil {
			t.Fatal("retryOptions() = nil, want 既定値つきの設定")
		}
		if *got.Attempts != int32(DefaultMaxRetries)+1 {
			t.Errorf("Attempts = %v, want %v（初回を含む総試行回数）", *got.Attempts, DefaultMaxRetries+1)
		}
		if *got.InitialDelay != DefaultInitialDelay.Seconds() || *got.MaxDelay != DefaultMaxDelay.Seconds() {
			t.Errorf("待ち時間の既定値が適用されていません: %+v", got)
		}
	})

	t.Run("設定値で上書きされること", func(t *testing.T) {
		got := Config{
			MaxRetries:   5,
			InitialDelay: 10 * time.Second,
			MaxDelay:     60 * time.Second,
		}.retryOptions()

		if *got.Attempts != 6 {
			t.Errorf("Attempts = %v, want 6（初回 + リトライ 5 回）", *got.Attempts)
		}
		if *got.InitialDelay != 10 || *got.MaxDelay != 60 {
			t.Errorf("待ち時間が正しく適用されていません: %+v", got)
		}
	})

	// SDK の既定ジッタ（U(0, 1秒) の加算）は InitialDelay が数十秒だとほぼ効かないため、
	// 初期間隔に比例した幅を明示します。
	t.Run("ジッタが初期間隔の半分になること", func(t *testing.T) {
		got := Config{InitialDelay: 60 * time.Second}.retryOptions()
		if *got.Jitter != 30 {
			t.Errorf("Jitter = %v, want 30", *got.Jitter)
		}
	})

	// nil は SDK 側で「1 回だけ実行」を意味します。値型の MaxRetries では
	// ゼロ値と「リトライしない」を区別できないため、専用のフラグで表します。
	t.Run("DisableRetry で nil になること", func(t *testing.T) {
		if got := (Config{DisableRetry: true}).retryOptions(); got != nil {
			t.Errorf("retryOptions() = %+v, want nil", got)
		}
	})

	t.Run("MaxRetries の 0 は未設定として既定値へ倒すこと", func(t *testing.T) {
		got := Config{MaxRetries: 0}.retryOptions()
		if *got.Attempts != int32(DefaultMaxRetries)+1 {
			t.Errorf("Attempts = %v, want %v", *got.Attempts, DefaultMaxRetries+1)
		}
	})
}

// TestAttemptsFromSaturates は、int32 に収まらないリトライ回数で総試行回数が
// 負に回り込まないことを検証します。回り込むと SDK が 1 回も実行しません。
func TestAttemptsFromSaturates(t *testing.T) {
	tests := []struct {
		name       string
		maxRetries uint
		want       int32
	}{
		{"通常の値は +1 されること", 3, 4},
		{"int32 の上限を超える指定は飽和すること", math.MaxUint32, math.MaxInt32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := attemptsFrom(tt.maxRetries); got != tt.want {
				t.Errorf("attemptsFrom(%d) = %d, want %d", tt.maxRetries, got, tt.want)
			}
		})
	}
}

// TestNoRetryHTTPOptions は、リクエスト単位でリトライを打ち消す設定を検証します。
// genai はリクエスト側の RetryOptions が非 nil のときだけクライアント設定を
// 置き換えるため、打ち消しには nil ではなく Attempts=1 を渡す必要があります。
func TestNoRetryHTTPOptions(t *testing.T) {
	got := noRetryHTTPOptions()
	if got.RetryOptions == nil || *got.RetryOptions.Attempts != 1 {
		t.Errorf("noRetryHTTPOptions() = %+v, want Attempts=1", got.RetryOptions)
	}
}

// TestToClientConfigAttachesCredentialsToSuppliedHTTPClient は、HTTPClient を指定しても
// Vertex AI の認証が効くことを検証します。
//
// genai は ClientConfig.HTTPClient が非 nil だと ADC の検出をスキップし、渡された
// クライアントを認証ヘッダ無しで使います。そのため素の &http.Client{Timeout: ...} を
// 渡すと全リクエストが 401 (CREDENTIALS_MISSING) になります。実際にこれで本番が
// 停止したため、Transport が差し替えられている（＝認証が付いている）ことを確認します。
func TestToClientConfigAttachesCredentialsToSuppliedHTTPClient(t *testing.T) {
	skipWithoutGCPCredentials(t)

	supplied := &http.Client{Timeout: 42 * time.Second}
	cfg := Config{ProjectID: "p", LocationID: "asia-northeast1", HTTPClient: supplied}

	got, err := cfg.toClientConfig()
	if err != nil {
		t.Fatalf("toClientConfig() error = %v", err)
	}
	if got.HTTPClient == nil {
		t.Fatal("HTTPClient = nil, want 渡したクライアント")
	}
	if got.HTTPClient.Timeout != 42*time.Second {
		t.Errorf("Timeout = %v, want 指定値が残ること", got.HTTPClient.Timeout)
	}
	if got.HTTPClient.Transport == nil {
		t.Error("Transport = nil, want 認証ミドルウェアが付くこと")
	}
	// 呼び出し側のインスタンスは書き換えない（他所で使い回されている可能性がある）。
	if supplied.Transport != nil {
		t.Error("呼び出し側の http.Client が書き換えられています。複製すべきです")
	}
	if got.HTTPClient == supplied {
		t.Error("HTTPClient が呼び出し側のインスタンスそのものです。複製すべきです")
	}
}
