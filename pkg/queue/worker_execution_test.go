package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"
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
			if !strings.Contains(logs, "DEBUG eventqueue.task.execution.started") ||
				!strings.Contains(logs, "DEBUG eventqueue.task.execution.finished") ||
				!strings.Contains(logs, "result"+tc.result) ||
				!strings.Contains(logs, "duration") {
				t.Fatalf("incomplete execution lifecycle log: %s", logs)
			}
			if strings.Contains(logs, "INFO eventqueue.task.execution.") || strings.Contains(logs, "data") || strings.Contains(logs, "token") {
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
