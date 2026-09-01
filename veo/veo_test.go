package veo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/shouni/genai-kit/gemini"
)

// fakeGenerator は gemini.VideoGenerator のテストダブルです。genai SDK も GCP 認証も
// 使わずにポーリングの挙動を検証できることが、DI で境界を切っている利点そのものです。
type fakeGenerator struct {
	startOp  *gemini.VideoOperation
	startErr error

	// polls は PollVideo が返す応答を順に消費します。最後の要素に到達したら
	// 以降はそれを返し続けます。
	polls     []pollResponse
	pollCalls int
	lastName  string
}

type pollResponse struct {
	op  *gemini.VideoOperation
	err error
}

func (f *fakeGenerator) StartVideo(_ context.Context, _ string, _ gemini.VideoRequest) (*gemini.VideoOperation, error) {
	if f.startErr != nil {
		return nil, f.startErr
	}
	return f.startOp, nil
}

func (f *fakeGenerator) PollVideo(_ context.Context, operationName string) (*gemini.VideoOperation, error) {
	f.lastName = operationName
	i := f.pollCalls
	f.pollCalls++
	if i >= len(f.polls) {
		i = len(f.polls) - 1
	}
	return f.polls[i].op, f.polls[i].err
}

// newTestClient は、テストが待たされないよう短いポーリング間隔で Client を作ります。
// 仮想時計を使うテストでも実時間を消費しないため、値そのものに意味はありません。
// ログは捨てます（ポーリングの警告が t の出力に混ざると読みにくいため）。
func newTestClient(t *testing.T, generator gemini.VideoGenerator, opts ...Option) *Client {
	t.Helper()

	base := []Option{
		WithPollInterval(time.Millisecond),
		WithPollTimeout(2 * time.Second),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	c, err := New(generator, append(base, opts...)...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return c
}

func running(name string) *gemini.VideoOperation {
	return &gemini.VideoOperation{Name: name}
}

func finished(name string, uris ...string) *gemini.VideoOperation {
	op := &gemini.VideoOperation{Name: name, Done: true}
	for _, uri := range uris {
		op.Videos = append(op.Videos, gemini.Attachment{URI: uri, MIMEType: "video/mp4"})
	}
	return op
}

func TestNewRequiresGenerator(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, ErrGeneratorRequired) {
		t.Fatalf("New(nil) error = %v, want ErrGeneratorRequired", err)
	}
}

// TestGeneratePollsUntilDone は、投函後に完了するまでポーリングし、完了した時点の
// 結果を返すことを検証します。
func TestGeneratePollsUntilDone(t *testing.T) {
	generator := &fakeGenerator{
		startOp: running("operations/abc"),
		polls: []pollResponse{
			{op: running("operations/abc")},
			{op: running("operations/abc")},
			{op: finished("operations/abc", "gs://bucket/out.mp4")},
		},
	}
	client := newTestClient(t, generator)

	got, err := client.Generate(context.Background(), "veo-3.1-generate-001", Request{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if got.OperationName != "operations/abc" {
		t.Errorf("OperationName = %q", got.OperationName)
	}
	if generator.lastName != "operations/abc" {
		t.Errorf("polled name = %q, want 投函したオペレーション名", generator.lastName)
	}
	video, ok := got.First()
	if !ok || video.URI != "gs://bucket/out.mp4" {
		t.Errorf("First() = %+v, %v", video, ok)
	}
	if generator.pollCalls != 3 {
		t.Errorf("poll calls = %d, want 3", generator.pollCalls)
	}
}

// TestGenerateReturnsImmediatelyWhenAlreadyDone は、投函の応答が既に完了していた場合に
// 1 度もポーリングせず結果を返すことを検証します。
func TestGenerateReturnsImmediatelyWhenAlreadyDone(t *testing.T) {
	generator := &fakeGenerator{startOp: finished("operations/done", "gs://bucket/out.mp4")}
	client := newTestClient(t, generator)

	if _, err := client.Generate(context.Background(), "veo-3.1-generate-001", Request{Prompt: "a cat"}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if generator.pollCalls != 0 {
		t.Errorf("poll calls = %d, want 0", generator.pollCalls)
	}
}

// TestGeneratePropagatesStartError は、投函に失敗したらポーリングへ進まないことを
// 検証します。リトライは gemini 側で既に尽きているため、ここでは再試行しません。
func TestGeneratePropagatesStartError(t *testing.T) {
	sentinel := errors.New("quota exceeded")
	generator := &fakeGenerator{startErr: sentinel}
	client := newTestClient(t, generator)

	_, err := client.Generate(context.Background(), "veo-3.1-generate-001", Request{Prompt: "a cat"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Generate() error = %v, want 投函時のエラー", err)
	}
	if generator.pollCalls != 0 {
		t.Errorf("poll calls = %d, want 0", generator.pollCalls)
	}
}

// TestGenerateRejectsEmptyStartResponse は、投函の応答が nil だった場合に
// 空レスポンスとして扱うことを検証します。
func TestGenerateRejectsEmptyStartResponse(t *testing.T) {
	client := newTestClient(t, &fakeGenerator{startOp: nil})

	_, err := client.Generate(context.Background(), "veo-test", Request{Prompt: "a cat"})
	if !errors.Is(err, gemini.ErrEmptyResponse) {
		t.Fatalf("Generate() error = %v, want ErrEmptyResponse", err)
	}
}

// TestWaitPollsImmediately は、待ちの再開で 1 interval 分の死に時間を作らないことを
// 検証します。別実行から再開する経路では対象が既に完了していることが多い前提です。
func TestWaitPollsImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		generator := &fakeGenerator{polls: []pollResponse{{op: finished("operations/abc", "gs://bucket/out.mp4")}}}
		client := newTestClient(t, generator, WithPollInterval(10*time.Second))

		start := time.Now()
		if _, err := client.Wait(context.Background(), "operations/abc"); err != nil {
			t.Fatalf("Wait() error = %v", err)
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Errorf("elapsed = %v, want 0（間隔を待たずに 1 回目を撃つ）", elapsed)
		}
	})
}

// TestWaitStopsAfterConsecutivePollErrors は、確認が続けて失敗したらタイムアウトまで
// 粘らずに打ち切ることを検証します。原因は Unwrap で辿れます。
func TestWaitStopsAfterConsecutivePollErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sentinel := errors.New("permission denied")
		generator := &fakeGenerator{polls: []pollResponse{{err: sentinel}}}
		client := newTestClient(t, generator, WithMaxPollErrors(3))

		_, err := client.Wait(context.Background(), "operations/abc")
		if !errors.Is(err, ErrPollFailed) {
			t.Fatalf("Wait() error = %v, want ErrPollFailed", err)
		}
		if !errors.Is(err, sentinel) {
			t.Errorf("Wait() error = %v, want 原因が辿れること", err)
		}
		if generator.pollCalls != 3 {
			t.Errorf("poll calls = %d, want 許容回数の 3 で打ち切ること", generator.pollCalls)
		}
	})
}

// TestWaitTimesOut は、生成が終わらないまま上限時間に達した場合に打ち切ることを
// 検証します。時間切れは一時的な失敗とは別物です。
func TestWaitTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		generator := &fakeGenerator{polls: []pollResponse{{op: running("operations/abc")}}}
		client := newTestClient(t, generator, WithPollTimeout(20*time.Millisecond))

		_, err := client.Wait(context.Background(), "operations/abc")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Wait() error = %v, want 期限切れ", err)
		}
		if errors.Is(err, ErrPollFailed) {
			t.Error("時間切れはポーリングの失敗ではないため ErrPollFailed に分類すべきではありません")
		}
	})
}

// TestWaitPropagatesGenerationFailure は、生成そのものが失敗として完了した場合に、
// 通信エラーではなく生成失敗として返すことを検証します。
func TestWaitPropagatesGenerationFailure(t *testing.T) {
	failed := &gemini.VideoOperation{
		Name:    "operations/abc",
		Done:    true,
		Failure: fmt.Errorf("%w: code=3: INVALID_ARGUMENT", gemini.ErrVideoGenerationFailed),
	}
	client := newTestClient(t, &fakeGenerator{polls: []pollResponse{{op: failed}}})

	_, err := client.Wait(context.Background(), "operations/abc")
	if !errors.Is(err, gemini.ErrVideoGenerationFailed) {
		t.Fatalf("Wait() error = %v, want ErrVideoGenerationFailed", err)
	}
}

// TestWaitReportsSafetyFiltering は、成功で完了したのに動画が無い場合に、除外の
// 理由まで含めて報告することを検証します。プロンプトを直すのに必要な情報です。
func TestWaitReportsSafetyFiltering(t *testing.T) {
	filtered := &gemini.VideoOperation{
		Name:            "operations/abc",
		Done:            true,
		FilteredCount:   1,
		FilteredReasons: []string{"violence"},
	}
	client := newTestClient(t, &fakeGenerator{polls: []pollResponse{{op: filtered}}})

	_, err := client.Wait(context.Background(), "operations/abc")
	if !errors.Is(err, ErrNoVideoGenerated) {
		t.Fatalf("Wait() error = %v, want ErrNoVideoGenerated", err)
	}
	if !strings.Contains(err.Error(), "violence") {
		t.Errorf("error = %q, want 除外理由を含むこと", err)
	}
}

// TestWaitReportsMissingVideoWithoutReason は、除外理由が無いまま動画が返らなかった
// 場合でもオペレーション名を残すことを検証します。
func TestWaitReportsMissingVideoWithoutReason(t *testing.T) {
	empty := &gemini.VideoOperation{Name: "operations/abc", Done: true}
	client := newTestClient(t, &fakeGenerator{polls: []pollResponse{{op: empty}}})

	_, err := client.Wait(context.Background(), "operations/abc")
	if !errors.Is(err, ErrNoVideoGenerated) {
		t.Fatalf("Wait() error = %v, want ErrNoVideoGenerated", err)
	}
	if !strings.Contains(err.Error(), "operations/abc") {
		t.Errorf("error = %q, want オペレーション名を含むこと", err)
	}
}

func TestWaitRequiresOperationName(t *testing.T) {
	client := newTestClient(t, &fakeGenerator{})

	if _, err := client.Wait(context.Background(), "  "); !errors.Is(err, ErrMissingOperationName) {
		t.Fatalf("Wait(空白) error = %v, want ErrMissingOperationName", err)
	}
}

// TestSubmitReturnsOperationName は、投函だけを単独で使えることを検証します。
// これが無いと、待ちを別実行に回す呼び出し側が投函だけ veo を通らず
// gemini.StartVideo を直接呼ぶことになり、投函と待ちで依存先が割れます。
func TestSubmitReturnsOperationName(t *testing.T) {
	fake := &fakeGenerator{startOp: running("operations/submitted")}
	client := newTestClient(t, fake)

	name, err := client.Submit(context.Background(), "veo-test", Request{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if name != "operations/submitted" {
		t.Errorf("name = %q, want オペレーション名", name)
	}
	if fake.pollCalls != 0 {
		t.Errorf("PollVideo calls = %d, want 0（Submit は待たない）", fake.pollCalls)
	}
}

// TestSubmitRequiresOperationName は、名前の無いオペレーションを投函時に弾くことを
// 検証します。Wait でポーリングできない名前を返すよりも早く失敗させます。
func TestSubmitRequiresOperationName(t *testing.T) {
	client := newTestClient(t, &fakeGenerator{startOp: &gemini.VideoOperation{}})

	_, err := client.Submit(context.Background(), "veo-test", Request{Prompt: "a cat"})
	if !errors.Is(err, ErrMissingOperationName) {
		t.Errorf("Submit() error = %v, want ErrMissingOperationName", err)
	}
}

func TestSubmitPropagatesStartError(t *testing.T) {
	sentinel := errors.New("boom")
	client := newTestClient(t, &fakeGenerator{startErr: sentinel})

	_, err := client.Submit(context.Background(), "veo-test", Request{Prompt: "a cat"})
	if !errors.Is(err, sentinel) {
		t.Errorf("Submit() error = %v, want 投函時のエラー", err)
	}
}

// TestResultFirst は、動画が無いときに添字アクセスで落ちないことを検証します。
func TestResultFirst(t *testing.T) {
	tests := []struct {
		name   string
		result *Result
		wantOK bool
	}{
		{"nil レシーバ", nil, false},
		{"動画なし", &Result{}, false},
		{"動画あり", &Result{Videos: []gemini.Attachment{{URI: "gs://bucket/out.mp4"}}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.result.First()
			if ok != tt.wantOK {
				t.Fatalf("First() ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok && (got.URI != "" || got.MIMEType != "" || len(got.Data) != 0) {
				t.Errorf("First() = %+v, want ゼロ値", got)
			}
		})
	}
}
