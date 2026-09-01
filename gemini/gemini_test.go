package gemini

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestNewRejectsIncompleteConfig は、設定の不備が genai クライアントの構築より前に
// 弾かれることを検証します。validate が先に走るため、この経路は ADC を必要としません。
//
// 「両方空」と「片方だけ」を別のセンチネルで返すのは、設定を渡し忘れたのか
// 環境変数が空だったのかという別々の間違いを、呼び出し側が区別できるようにするためです。
func TestNewRejectsIncompleteConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want error
	}{
		{"設定が空", Config{}, ErrConfigRequired},
		{"ProjectID のみ", Config{ProjectID: "my-project"}, ErrIncompleteVertexConfig},
		{"LocationID のみ", Config{LocationID: "asia-northeast1"}, ErrIncompleteVertexConfig},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(context.Background(), tt.cfg)
			if !errors.Is(err, tt.want) {
				t.Fatalf("New() error = %v, want %v", err, tt.want)
			}
			if client != nil {
				t.Errorf("New() client = %+v, want nil", client)
			}
		})
	}
}

// TestNewBuildsVertexClient は、正しい設定から Vertex AI のクライアントが組み立てられ、
// SDK の呼び出し面（生成と動画）が両方とも埋まることを検証します。
func TestNewBuildsVertexClient(t *testing.T) {
	skipWithoutGCPCredentials(t)

	client, err := New(context.Background(), Config{
		ProjectID:      "my-project",
		LocationID:     "asia-northeast1",
		RequestTimeout: 90 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if client.modelClient == nil || client.videoClient == nil {
		t.Errorf("client = %+v, want both SDK 呼び出し面が埋まっていること", client)
	}
	if client.requestTimeout != 90*time.Second {
		t.Errorf("requestTimeout = %v, want 90s", client.requestTimeout)
	}
}

// TestClientRequestContext は、RequestTimeout が設定されたときだけ期限を足すことを
// 検証します。0 は無制限で、呼び出し側の context の期限にのみ従います。
func TestClientRequestContext(t *testing.T) {
	t.Run("未設定なら呼び出し側の context をそのまま使う", func(t *testing.T) {
		ctx, cancel := (&Client{}).requestContext(context.Background())
		defer cancel()

		if _, ok := ctx.Deadline(); ok {
			t.Error("期限が付いています。0 は無制限であるべきです")
		}
	})

	t.Run("設定されていれば期限が付く", func(t *testing.T) {
		ctx, cancel := (&Client{requestTimeout: time.Minute}).requestContext(context.Background())
		defer cancel()

		if _, ok := ctx.Deadline(); !ok {
			t.Error("期限が付いていません")
		}
	})
}
