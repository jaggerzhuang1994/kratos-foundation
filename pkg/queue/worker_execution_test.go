package queue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	kratostracing "github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"go.opentelemetry.io/otel/trace"
)

func TestExecutionPersistsOutcome(t *testing.T) {
	for _, tc := range []struct {
		name    string
		attempt int
		handler taskHandler
		want    string
		result  string
	}{
		{"success", 1, func(_ context.Context, t *Task) error { t.Payload[0] = 'X'; return nil }, "ack", "success"},
		{"retry", 1, func(context.Context, *Task) error { return errors.New("temporary") }, "release", "retry"},
		{"exhausted", 3, func(context.Context, *Task) error { return errors.New("temporary") }, "retry_exhausted", "failed"},
		{"crashed repeatedly", 4, func(context.Context, *Task) error { panic("must not run") }, "retry_exhausted", "failed"},
		{"permanent", 1, func(context.Context, *Task) error { return Permanent(errors.New("invalid")) }, "permanent", "failed"},
		{"unknown type", 1, nil, "permanent", "failed"},
		{"panic", 1, func(context.Context, *Task) error { panic("handler panic value") }, "release", "retry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obs, _ := testObservability(t)
			outcome := ""
			var at time.Time
			store := &storeStub{ack: func(context.Context, *Reservation) error { outcome = "ack"; return nil }, release: func(_ context.Context, _ *Reservation, n time.Time) error { outcome = "release"; at = n; return nil }, fail: func(_ context.Context, _ *Reservation, r string, _ time.Time) error { outcome = r; return nil }}
			handlers := map[string]taskHandler{"other": func(context.Context, *Task) error { return nil }}
			if tc.handler != nil {
				handlers["email"] = tc.handler
			}
			worker, err := newWorker[*Task](workerConfig{Name: "test", Queue: "mail"}, store, handlers, obs)
			if err != nil {
				t.Fatal(err)
			}
			r := &Reservation{Task: &Task{ID: "one", MessageVersion: "email", Payload: []byte("data")}, Token: "token", Attempts: tc.attempt}
			before := time.Now()
			if err = worker.execute(context.Background(), r, before); err != nil {
				t.Fatal(err)
			}
			if outcome != tc.want || string(r.Task.Payload) != "data" {
				t.Fatalf("outcome %s snapshot %s", outcome, r.Task.Payload)
			}
			logs := strings.Join(obs.Logger.(*testLog).events, "\n")
			if !strings.Contains(logs, "INFO eventqueue.task.execution.started") ||
				!strings.Contains(logs, "INFO eventqueue.task.execution.finished") ||
				!strings.Contains(logs, "result"+tc.result) ||
				!strings.Contains(logs, "duration") {
				t.Fatalf("incomplete execution lifecycle log: %s", logs)
			}
			// 开始与结束各记一条 Info，不记录 Payload 或租约 Token。
			if strings.Count(logs, "eventqueue.task.execution.") != 2 || strings.Contains(logs, "data") || strings.Contains(logs, "token") {
				t.Fatalf("noisy or unsafe execution log: %s", logs)
			}
			if tc.name != "success" {
				cause := map[string]string{"retry": "handler_error", "exhausted": "handler_error", "crashed repeatedly": "attempts_exhausted", "permanent": "handler_error", "unknown type": "handler_missing", "panic": "panic"}[tc.name]
				detail := map[string]string{"retry": "temporary", "exhausted": "temporary", "crashed repeatedly": "queue attempts exhausted before execution", "permanent": "permanent queue error: invalid", "unknown type": "permanent queue error: queue task message version is not registered", "panic": "queue handler panicked: handler panic value\ngoroutine"}[tc.name]
				if !strings.Contains(logs, "error"+cause) || !strings.Contains(logs, "task.message_versionemail") || !strings.Contains(logs, "error.detail"+detail) {
					t.Fatalf("incomplete failure log: %s", logs)
				}
			} else if strings.Contains(logs, "error.detail") {
				t.Fatalf("success logged an error detail: %s", logs)
			}
			if outcome == "release" && at.Before(before.Add(500*time.Millisecond)) {
				t.Fatal("retry was not delayed")
			}
		})
	}
}

func TestExecutionTimeoutAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs, _ := testObservability(t)
		released := false
		store := &storeStub{release: func(context.Context, *Reservation, time.Time) error { released = true; return nil }}
		worker, err := newWorker[*Task](workerConfig{Name: "test", Queue: "mail", Timeout: time.Second}, store, map[string]taskHandler{"email": func(ctx context.Context, _ *Task) error { <-ctx.Done(); return nil }}, obs)
		if err != nil {
			t.Fatal(err)
		}
		r := &Reservation{Task: &Task{ID: "one", MessageVersion: "email"}, Token: "t", Attempts: 1}
		if err = worker.execute(context.Background(), r, time.Now()); err != nil || !released {
			t.Fatalf("timeout %v released=%v", err, released)
		}
		// 执行 Context 被取消（停机中止）时立即释放，不按失败退避。
		var releasedAt time.Time
		store.release = func(_ context.Context, _ *Reservation, at time.Time) error {
			released, releasedAt = true, at
			return nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		released = false
		if err = worker.execute(ctx, r, time.Now()); err != nil || !released || !releasedAt.Equal(time.Now().UTC()) {
			t.Fatalf("cancel %v released=%v at %v", err, released, releasedAt)
		}
		logs := strings.Join(obs.Logger.(*testLog).events, "\n")
		if !strings.Contains(logs, "resultstopped") || !strings.Contains(logs, "duration") {
			t.Fatalf("canceled lifecycle log: %s", logs)
		}
		if err = worker.execute(context.Background(), &Reservation{}, time.Now()); err == nil {
			t.Fatal("accepted invalid reservation")
		}
	})
}

func TestExecutionStorageFailureAndBackoff(t *testing.T) {
	obs, _ := testObservability(t)
	failure := errors.New("store failed")
	store := &storeStub{release: func(_ context.Context, _ *Reservation, at time.Time) error {
		if at.Before(time.Now().Add(900 * time.Millisecond)) {
			t.Fatal("backoff not doubled")
		}
		return failure
	}}
	w, err := newWorker[*Task](workerConfig{Name: "test", Queue: "mail"}, store, map[string]taskHandler{"x": func(context.Context, *Task) error { return errors.New("retry") }}, obs)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.execute(context.Background(), &Reservation{Task: &Task{MessageVersion: "x"}, Token: "t", Attempts: 2}, time.Now()); !errors.Is(err, failure) {
		t.Fatalf("lost failure %v", err)
	}
	logs := strings.Join(obs.Logger.(*testLog).events, "\n")
	if !strings.Contains(logs, "resultstorage_error") || !strings.Contains(logs, "duration") {
		t.Fatalf("storage failure lifecycle log: %s", logs)
	}
}

func TestFailureNotificationAfterArchive(t *testing.T) {
	for _, tc := range []struct {
		name          string
		archiveErr    error
		attempt       int
		payload       string
		callbackPanic bool
	}{
		{name: "bad message", attempt: 1, payload: "{"},
		{name: "retry exhausted", attempt: 3, payload: `"valid"`},
		{name: "claim exhausted", attempt: 4, payload: `"valid"`},
		{name: "archive offline", archiveErr: errors.New("offline"), attempt: 1, payload: "{"},
		{name: "lease lost", archiveErr: ErrLeaseLost, attempt: 1, payload: "{"},
		{name: "callback panic", attempt: 1, payload: "{", callbackPanic: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obs, _ := testObservability(t)
			archived, notified := false, false
			store := &storeStub{fail: func(context.Context, *Reservation, string, time.Time) error {
				archived = tc.archiveErr == nil
				return tc.archiveErr
			}}
			q, err := buildTestWorker(Definition[string]{Queue: "q", Version: 1}, store, func(context.Context, string) error { return errors.New("temporary") }, WorkerConfig{OnFailed: func(ctx context.Context, event FailureEvent) error {
				notified = true
				if !archived || event.Task.ID != "id" || event.Attempts != tc.attempt || event.MaxAttempts != 3 || event.FailedAt.IsZero() {
					t.Errorf("event before archive or wrong event: %+v", event)
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Error("callback lacks timeout")
				}
				wantCause := "decode_error"
				if tc.attempt == 3 {
					wantCause = "handler_error"
				}
				if tc.attempt == 4 {
					wantCause = "attempts_exhausted"
				}
				if event.Cause != wantCause {
					t.Errorf("cause=%s want=%s", event.Cause, wantCause)
				}
				event.Task.ID = "changed"
				if tc.callbackPanic {
					panic("private callback data")
				}
				return errors.New("private callback error")
			}}, obs)
			if err != nil {
				t.Fatal(err)
			}
			r := &Reservation{Task: &Task{ID: "id", MessageVersion: "string.v1", Payload: []byte(tc.payload)}, Token: "token", Attempts: tc.attempt}
			err = q.execute(context.Background(), r, time.Now())
			if !errors.Is(err, tc.archiveErr) || notified != archived || r.Task.ID != "id" {
				t.Fatalf("err=%v notified=%v archived=%v task=%s", err, notified, archived, r.Task.ID)
			}
			logs := strings.Join(obs.Logger.(*testLog).events, "\n")
			wantReason := "reasonerror"
			if tc.callbackPanic {
				wantReason = "reasonpanic"
			}
			if strings.Contains(logs, "private callback") || (notified && (!strings.Contains(logs, "queue.failure.callback_failed") || !strings.Contains(logs, wantReason))) {
				t.Fatalf("callback logs: %s", logs)
			}
		})
	}
}

func TestFailureCallbackSuccessAndTimeout(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			obs, _ := testObservability(t)
			worker := &Worker[string]{onFailed: func(ctx context.Context, _ FailureEvent) error {
				if timeout {
					<-ctx.Done()
				}
				return nil
			}, log: obs.Logger, config: workerConfig{StorageTimeout: time.Second}}
			worker.notifyFailure(context.Background(), FailureEvent{FailedTask: FailedTask{Task: &Task{ID: "id"}}})
			logs := strings.Join(obs.Logger.(*testLog).events, "\n")
			logged := strings.Contains(logs, "queue.failure.callback_failed")
			if logged != timeout || (timeout && !strings.Contains(logs, "reasontimeout")) {
				t.Fatalf("callback failure logged=%v timeout=%v", logged, timeout)
			}
		})
	}
}

// recorderStub 在 storeStub 之上实现 ErrorRecorder，记录 Worker 写入的执行错误。
type recorderStub struct {
	*storeStub
	released, failed []ExecutionError
	reasons          []string
}

func (s *recorderStub) ReleaseWithError(_ context.Context, _ *Reservation, at time.Time, executionError ExecutionError) error {
	if at.Before(executionError.At) {
		return errors.New("retry scheduled before the failure")
	}
	s.released = append(s.released, executionError)
	return nil
}

func (s *recorderStub) FailWithError(_ context.Context, _ *Reservation, reason string, at time.Time, executionError ExecutionError) error {
	if !at.Equal(executionError.At) {
		return errors.New("failure time differs from error time")
	}
	s.failed = append(s.failed, executionError)
	s.reasons = append(s.reasons, reason)
	return nil
}

func TestExecutionRecordsErrorDetail(t *testing.T) {
	obs, _ := testObservability(t)
	// storeStub 未设置 release/fail：若走普通 Release/Fail 会因 nil 函数 panic，证明使用了 ErrorRecorder。
	store := &recorderStub{storeStub: &storeStub{ack: func(context.Context, *Reservation) error { return nil }}}
	var event FailureEvent
	handlerErr := fmt.Errorf("call provider: %w", errors.New("balance \xff too low"))
	q, err := buildTestWorker(Definition[string]{Queue: "q", Version: 1}, store, func(_ context.Context, message string) error {
		if message == "ok" {
			return nil
		}
		return handlerErr
	}, WorkerConfig{OnFailed: func(_ context.Context, e FailureEvent) error { event = e; return nil }}, obs)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		r := &Reservation{Task: &Task{ID: "id", MessageVersion: "string.v1", Payload: []byte(`"bad"`)}, Token: "token", Attempts: attempt}
		if err = q.execute(context.Background(), r, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	want := "call provider: balance � too low"
	if len(store.released) != 2 || len(store.failed) != 1 || store.reasons[0] != "retry_exhausted" {
		t.Fatalf("released=%v failed=%v reasons=%v", store.released, store.failed, store.reasons)
	}
	for _, recorded := range append(store.released, store.failed...) {
		if recorded.Message != want || recorded.At.IsZero() || recorded.At.Location() != time.UTC {
			t.Fatalf("recorded %+v", recorded)
		}
	}
	if event.LastError != want || !event.LastErrorAt.Equal(store.failed[0].At) {
		t.Fatalf("failure event %+v", event)
	}
	ok := &Reservation{Task: &Task{ID: "id", MessageVersion: "string.v1", Payload: []byte(`"ok"`)}, Token: "token", Attempts: 1}
	if err = q.execute(context.Background(), ok, time.Now()); err != nil || len(store.released)+len(store.failed) != 3 {
		t.Fatalf("success touched the error record: %v", err)
	}
}

func TestExecutionTimeoutKeepsHandlerError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs, _ := testObservability(t)
		store := &recorderStub{storeStub: &storeStub{}}
		worker, err := newWorker[*Task](workerConfig{Name: "test", Queue: "mail", Timeout: time.Second}, store, map[string]taskHandler{"email": func(ctx context.Context, _ *Task) error {
			<-ctx.Done()
			return Permanent(fmt.Errorf("query upstream: %w", errors.New("connection reset")))
		}}, obs)
		if err != nil {
			t.Fatal(err)
		}
		if err = worker.execute(context.Background(), &Reservation{Task: &Task{ID: "one", MessageVersion: "email"}, Token: "t", Attempts: 1}, time.Now()); err != nil {
			t.Fatal(err)
		}
		// 超时仍按可重试处理（不受 Handler 返回的 Permanent 影响），错误原文保留 Handler 的说明。
		if len(store.released) != 1 || store.released[0].Message != "context deadline exceeded: permanent queue error: query upstream: connection reset" {
			t.Fatalf("released %+v failed %+v", store.released, store.failed)
		}
		logs := strings.Join(obs.Logger.(*testLog).events, "\n")
		if !strings.Contains(logs, "errortimeout") {
			t.Fatalf("timeout cause missing: %s", logs)
		}
	})
}

func TestErrorMessageTruncatesOnRuneBoundary(t *testing.T) {
	if got := errorMessage(errors.New("short")); got != "short" {
		t.Fatalf("short message %q", got)
	}
	long := errorMessage(errors.New(strings.Repeat("a", MaxErrorMessageBytes-15) + strings.Repeat("错", 100)))
	if len(long) > MaxErrorMessageBytes || !utf8.ValidString(long) || !strings.HasSuffix(long, "...(truncated)") {
		t.Fatalf("truncated message len=%d valid=%v", len(long), utf8.ValidString(long))
	}
}

// 同一次请求批量投递的任务各自一条 trace（以 Link 关联投递方）：trace.id 由任务 ID 确定，重试共用；
// Handler 内通过 Context 输出的业务日志带同一个 trace.id 与任务定位字段，重试时另带 task.retry。
func TestExecutionUsesOneTracePerTaskLinkedToProducer(t *testing.T) {
	obs, exporter := testObservability(t)
	logPath := filepath.Join(t.TempDir(), "queue.log")
	logger, cleanup, err := testlog.New(testlog.Config{
		Level:       kratoslog.LevelDebug,
		FilterEmpty: true,
		TimeFormat:  time.RFC3339,
		Std:         testlog.OutputConfig{Disable: true, Level: kratoslog.LevelDebug},
		File: testlog.FileConfig{
			OutputConfig: testlog.OutputConfig{Level: kratoslog.LevelDebug},
			Path:         logPath,
			Rotating:     testlog.RotatingConfig{Disable: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	// 与 bootstrap 相同：trace.id 由 Context 中的 span 求值。
	logger = logger.With(foundationlog.TraceIDKey, kratostracing.TraceID())
	obs.Logger = logger

	// 一个投递方 span 内连续投递两个任务。
	stored := []*Task{}
	store := &storeStub{
		enqueue: func(_ context.Context, task *Task) error { stored = append(stored, task); return nil },
		ack:     func(context.Context, *Reservation) error { return nil },
		release: func(context.Context, *Reservation, time.Time) error { return nil },
	}
	dispatcher, err := newDispatcher("mail", store, obs)
	if err != nil {
		t.Fatal(err)
	}
	producerCtx, producerSpan := obs.Tracing.Tracer("test").Start(context.Background(), "cron")
	producerTrace := producerSpan.SpanContext().TraceID()
	for range 2 {
		if _, err = dispatcher.dispatch(producerCtx, &Task{MessageVersion: "email"}); err != nil {
			t.Fatal(err)
		}
	}
	producerSpan.End()

	handlerTraces := []trace.TraceID{}
	failFirstAttempt := true
	worker, err := newWorker[*Task](workerConfig{Name: "test", Queue: "mail", Timeout: time.Second}, store, map[string]taskHandler{"email": func(ctx context.Context, _ *Task) error {
		handlerTraces = append(handlerTraces, trace.SpanContextFromContext(ctx).TraceID())
		logger.WithContext(ctx).Info("business log")
		if failFirstAttempt {
			failFirstAttempt = false
			return errors.New("upstream unavailable")
		}
		return nil
	}}, obs)
	if err != nil {
		t.Fatal(err)
	}
	// 第一个任务首次执行失败后重试一次，第二个任务一次成功。
	for _, reservation := range []*Reservation{
		{Task: stored[0], Token: "t", Attempts: 1},
		{Task: stored[0], Token: "t", Attempts: 2},
		{Task: stored[1], Token: "t", Attempts: 1},
	} {
		if err = worker.execute(context.Background(), reservation, time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	if len(handlerTraces) != 3 || handlerTraces[0] != handlerTraces[1] {
		t.Fatalf("retry must reuse the task trace: %v", handlerTraces)
	}
	if handlerTraces[0] == handlerTraces[2] || handlerTraces[0] == producerTrace || handlerTraces[2] == producerTrace {
		t.Fatalf("tasks must not share a trace: producer=%s handlers=%v", producerTrace, handlerTraces)
	}
	// 任务 ID 是 UUID 时 trace.id 就是去掉连字符的任务 ID。
	if handlerTraces[0].String() != strings.ReplaceAll(stored[0].ID, "-", "") {
		t.Fatalf("trace %s is not derived from task id %s", handlerTraces[0], stored[0].ID)
	}
	handlerTraces = []trace.TraceID{handlerTraces[0], handlerTraces[2]}
	executions := 0
	for _, span := range exporter.GetSpans() {
		if span.Name != "queue.execute" {
			continue
		}
		executions++
		if span.Parent.TraceID() == producerTrace || len(span.Links) != 1 || span.Links[0].SpanContext.TraceID() != producerTrace {
			t.Fatalf("execute span parent=%v links=%v, want task trace linked to producer %s", span.Parent, span.Links, producerTrace)
		}
	}
	if executions != 3 {
		t.Fatalf("execute spans = %d, want 3", executions)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for index, task := range stored {
		lines, retryLines := 0, 0
		for line := range strings.SplitSeq(string(data), "\n") {
			if !strings.Contains(line, "task.id="+task.ID) {
				continue
			}
			lines++
			if strings.Contains(line, "task.retry=1") {
				retryLines++
			}
			// 框架日志与业务日志共用本次执行的 trace.id，任务定位字段不重复。
			if !strings.Contains(line, "trace.id="+handlerTraces[index].String()) || strings.Count(line, "task.id=") != 1 || strings.Count(line, "queue=mail") != 1 {
				t.Fatalf("task %d log line lacks its trace or duplicates fields: %s", index, line)
			}
		}
		// 每次执行 started、business log、finished 各一行；第一个任务多一行重试调度，且重试那次执行的三行都带 task.retry=1。
		wantLines, wantRetryLines := 3, 0
		if index == 0 {
			wantLines, wantRetryLines = 7, 3
		}
		if lines != wantLines || retryLines != wantRetryLines {
			t.Fatalf("task %d log lines = %d (retry %d), want %d (retry %d): %s", index, lines, retryLines, wantLines, wantRetryLines, data)
		}
	}
	if !strings.Contains(string(data), "dispatch.trace.id="+producerTrace.String()) || !strings.Contains(string(data), "business log") {
		t.Fatalf("producer trace or business log missing: %s", data)
	}
}
