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

// fakeGenerator is a test double for gemini.VideoGenerator. Being able to test the polling
// behaviour without the genai SDK or GCP credentials is exactly the benefit of cutting the boundary with DI.
type fakeGenerator struct {
	startOp  *gemini.VideoOperation
	startErr error

	// polls consumes the responses returned by PollVideo in order. Once the last element is reached,
	// it keeps returning that one.
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

// newTestClient builds a Client with a short polling interval so tests are not kept waiting.
// Tests using the virtual clock consume no real time either, so the values themselves do not matter.
// Logs are discarded (polling warnings mixed into t's output are hard to read).
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

// TestGeneratePollsUntilDone checks that after submitting, it polls until completion and returns
// the result at the point it completed.
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
		t.Errorf("polled name = %q, want the submitted operation name", generator.lastName)
	}
	video, ok := got.First()
	if !ok || video.URI != "gs://bucket/out.mp4" {
		t.Errorf("First() = %+v, %v", video, ok)
	}
	if generator.pollCalls != 3 {
		t.Errorf("poll calls = %d, want 3", generator.pollCalls)
	}
}

// TestGenerateReturnsImmediatelyWhenAlreadyDone checks that when the submission response is
// already complete, the result is returned without polling even once.
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

// TestGeneratePropagatesStartError checks that a failed submission does not proceed to polling.
// Retries have already been exhausted on the gemini side, so it does not retry here.
func TestGeneratePropagatesStartError(t *testing.T) {
	sentinel := errors.New("quota exceeded")
	generator := &fakeGenerator{startErr: sentinel}
	client := newTestClient(t, generator)

	_, err := client.Generate(context.Background(), "veo-3.1-generate-001", Request{Prompt: "a cat"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Generate() error = %v, want the submission error", err)
	}
	if generator.pollCalls != 0 {
		t.Errorf("poll calls = %d, want 0", generator.pollCalls)
	}
}

// TestGenerateRejectsEmptyStartResponse checks that a nil submission response is treated as
// an empty response.
func TestGenerateRejectsEmptyStartResponse(t *testing.T) {
	client := newTestClient(t, &fakeGenerator{startOp: nil})

	_, err := client.Generate(context.Background(), "veo-test", Request{Prompt: "a cat"})
	if !errors.Is(err, gemini.ErrEmptyResponse) {
		t.Fatalf("Generate() error = %v, want ErrEmptyResponse", err)
	}
}

// TestWaitPollsImmediately checks that resuming a wait does not create one interval of dead time.
// It assumes that on the path resuming from a different execution, the target has often already completed.
func TestWaitPollsImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		generator := &fakeGenerator{polls: []pollResponse{{op: finished("operations/abc", "gs://bucket/out.mp4")}}}
		client := newTestClient(t, generator, WithPollInterval(10*time.Second))

		start := time.Now()
		if _, err := client.Wait(context.Background(), "operations/abc"); err != nil {
			t.Fatalf("Wait() error = %v", err)
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Errorf("elapsed = %v, want 0 (fire the first query without waiting for the interval)", elapsed)
		}
	})
}

// TestWaitStopsAfterConsecutivePollErrors checks that repeated check failures give up without
// holding on until the timeout. The cause can be reached through Unwrap.
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
			t.Errorf("Wait() error = %v, want the cause to be reachable", err)
		}
		if generator.pollCalls != 3 {
			t.Errorf("poll calls = %d, want it to give up at the tolerated count of 3", generator.pollCalls)
		}
	})
}

// TestWaitTimesOut checks that the wait gives up when the time limit is reached before generation
// finishes. A timeout is different from a transient failure.
func TestWaitTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		generator := &fakeGenerator{polls: []pollResponse{{op: running("operations/abc")}}}
		client := newTestClient(t, generator, WithPollTimeout(20*time.Millisecond))

		_, err := client.Wait(context.Background(), "operations/abc")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Wait() error = %v, want a deadline exceeded", err)
		}
		if errors.Is(err, ErrPollFailed) {
			t.Error("a timeout is not a polling failure and should not be classified as ErrPollFailed")
		}
	})
}

// TestWaitPropagatesGenerationFailure checks that when generation itself completes as a failure,
// it is returned as a generation failure rather than a transport error.
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

// TestWaitReportsSafetyFiltering checks that when the operation completed successfully but there is
// no video, the report includes the reason for the exclusion. That is the information needed to fix the prompt.
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
		t.Errorf("error = %q, want it to include the exclusion reason", err)
	}
}

// TestWaitReportsMissingVideoWithoutReason checks that the operation name is kept even when no
// video came back and there is no exclusion reason.
func TestWaitReportsMissingVideoWithoutReason(t *testing.T) {
	empty := &gemini.VideoOperation{Name: "operations/abc", Done: true}
	client := newTestClient(t, &fakeGenerator{polls: []pollResponse{{op: empty}}})

	_, err := client.Wait(context.Background(), "operations/abc")
	if !errors.Is(err, ErrNoVideoGenerated) {
		t.Fatalf("Wait() error = %v, want ErrNoVideoGenerated", err)
	}
	if !strings.Contains(err.Error(), "operations/abc") {
		t.Errorf("error = %q, want it to include the operation name", err)
	}
}

func TestWaitRequiresOperationName(t *testing.T) {
	client := newTestClient(t, &fakeGenerator{})

	if _, err := client.Wait(context.Background(), "  "); !errors.Is(err, ErrMissingOperationName) {
		t.Fatalf("Wait(whitespace) error = %v, want ErrMissingOperationName", err)
	}
}

// TestSubmitReturnsOperationName checks that submitting can be used on its own.
// Without it, a caller that moves the wait to a different execution would submit by calling
// gemini.StartVideo directly instead of through veo, splitting the dependency between submitting and waiting.
func TestSubmitReturnsOperationName(t *testing.T) {
	fake := &fakeGenerator{startOp: running("operations/submitted")}
	client := newTestClient(t, fake)

	name, err := client.Submit(context.Background(), "veo-test", Request{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if name != "operations/submitted" {
		t.Errorf("name = %q, want the operation name", name)
	}
	if fake.pollCalls != 0 {
		t.Errorf("PollVideo calls = %d, want 0 (Submit does not wait)", fake.pollCalls)
	}
}

// TestSubmitRequiresOperationName checks that an operation without a name is refused at submission.
// It fails earlier than returning a name that Wait cannot poll.
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
		t.Errorf("Submit() error = %v, want the submission error", err)
	}
}

// TestResultFirst checks that it does not crash on an index access when there is no video.
func TestResultFirst(t *testing.T) {
	tests := []struct {
		name   string
		result *Result
		wantOK bool
	}{
		{"nil receiver", nil, false},
		{"no video", &Result{}, false},
		{"with video", &Result{Videos: []gemini.Attachment{{URI: "gs://bucket/out.mp4"}}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.result.First()
			if ok != tt.wantOK {
				t.Fatalf("First() ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok && (got.URI != "" || got.MIMEType != "" || len(got.Data) != 0) {
				t.Errorf("First() = %+v, want the zero value", got)
			}
		})
	}
}
