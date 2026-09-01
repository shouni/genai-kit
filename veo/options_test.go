package veo

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

// TestOptionsIgnoreInvalidValues は、不正な値（ゼロ以下・nil）を「指定なし」として
// 無視し、既定値を保つことを検証します。呼び出し側が設定値を組み立てるとき、
// 未設定のゼロ値がそのまま「間隔 0 の暴走」にならないための決まりです。
func TestOptionsIgnoreInvalidValues(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		name string
		opts []Option
		want Client
	}{
		{
			name: "未指定なら既定値のまま",
			opts: nil,
			want: Client{pollInterval: DefaultPollInterval, pollTimeout: DefaultPollTimeout, maxPollErrors: DefaultMaxPollErrors},
		},
		{
			name: "指定した値が反映されること",
			opts: []Option{WithPollInterval(3 * time.Second), WithPollTimeout(time.Minute), WithMaxPollErrors(5)},
			want: Client{pollInterval: 3 * time.Second, pollTimeout: time.Minute, maxPollErrors: 5},
		},
		{
			name: "ゼロ以下は無視して既定値を保つこと",
			opts: []Option{WithPollInterval(0), WithPollTimeout(-time.Second), WithMaxPollErrors(0)},
			want: Client{pollInterval: DefaultPollInterval, pollTimeout: DefaultPollTimeout, maxPollErrors: DefaultMaxPollErrors},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(&fakeGenerator{}, append(tt.opts, WithLogger(logger))...)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if got.pollInterval != tt.want.pollInterval {
				t.Errorf("pollInterval = %v, want %v", got.pollInterval, tt.want.pollInterval)
			}
			if got.pollTimeout != tt.want.pollTimeout {
				t.Errorf("pollTimeout = %v, want %v", got.pollTimeout, tt.want.pollTimeout)
			}
			if got.maxPollErrors != tt.want.maxPollErrors {
				t.Errorf("maxPollErrors = %d, want %d", got.maxPollErrors, tt.want.maxPollErrors)
			}
		})
	}
}

// TestWithLoggerIgnoresNil は、nil のロガーで既定のロガーを消さないことを検証します。
// 消えると、ポーリングの警告ログが出ないうえ nil 参照で落ちます。
func TestWithLoggerIgnoresNil(t *testing.T) {
	client, err := New(&fakeGenerator{}, WithLogger(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if client.logger == nil {
		t.Error("logger = nil, want slog.Default()")
	}
}
