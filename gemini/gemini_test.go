package gemini

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestNewRejectsIncompleteConfig checks that configuration mistakes are refused before the
// genai client is built. validate runs first, so this path does not need ADC.
//
// "Both empty" and "only one set" return different sentinels so that the caller can tell apart
// two different mistakes: forgetting to pass the config, and an empty environment variable.
func TestNewRejectsIncompleteConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want error
	}{
		{"empty config", Config{}, ErrConfigRequired},
		{"ProjectID only", Config{ProjectID: "my-project"}, ErrIncompleteVertexConfig},
		{"LocationID only", Config{LocationID: "asia-northeast1"}, ErrIncompleteVertexConfig},
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

// TestNewBuildsVertexClient checks that a valid config builds a Vertex AI client and that both
// SDK call surfaces (generation and video) are filled in.
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
		t.Errorf("client = %+v, want both SDK call surfaces filled in", client)
	}
	if client.requestTimeout != 90*time.Second {
		t.Errorf("requestTimeout = %v, want 90s", client.requestTimeout)
	}
}

// TestClientRequestContext checks that a deadline is added only when RequestTimeout is set.
// 0 means unlimited, following only the deadline of the caller's context.
func TestClientRequestContext(t *testing.T) {
	t.Run("uses the caller's context as is when unset", func(t *testing.T) {
		ctx, cancel := (&Client{}).requestContext(context.Background())
		defer cancel()

		if _, ok := ctx.Deadline(); ok {
			t.Error("a deadline is set; 0 should mean unlimited")
		}
	})

	t.Run("adds a deadline when set", func(t *testing.T) {
		ctx, cancel := (&Client{requestTimeout: time.Minute}).requestContext(context.Background())
		defer cancel()

		if _, ok := ctx.Deadline(); !ok {
			t.Error("no deadline is set")
		}
	})
}
