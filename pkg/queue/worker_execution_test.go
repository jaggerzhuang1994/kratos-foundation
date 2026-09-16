package queue

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestExecutionPersistsOutcome(t *testing.T) {
	for _, tc := range []struct {
		name    string
		attempt int
		handler taskHandler
		want    string
	}{
		{"success", 1, func(_ context.Context, t *Task) error { t.Payload[0] = 'X'; return nil }, "ack"},
		{"retry", 1, func(context.Context, *Task) error { return errors.New("temporary") }, "release"},
		{"exhausted", 3, func(context.Context, *Task) error { return errors.New("temporary") }, "retry_exhausted"},
		{"crashed repeatedly", 4, func(context.Context, *Task) error { panic("must not run") }, "retry_exhausted"},
		{"permanent", 1, func(context.Context, *Task) error { return Permanent(errors.New("invalid")) }, "permanent"},
		{"unknown type", 1, nil, "permanent"},
		{"panic", 1, func(context.Context, *Task) error { panic("private panic data") }, "release"},
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
			r := &Reservation{Task: &Task{ID: "one", Type: "email", Payload: []byte("data")}, Token: "token", Attempts: tc.attempt}
			before := time.Now()
			if err = worker.execute(context.Background(), r, before); err != nil {
				t.Fatal(err)
			}
			if outcome != tc.want || string(r.Task.Payload) != "data" {
				t.Fatalf("outcome %s snapshot %s", outcome, r.Task.Payload)
			}
			if tc.name != "success" {
				logs := strings.Join(obs.Logger.(*testLog).events, "\n")
				cause := map[string]string{"retry": "handler_error", "exhausted": "handler_error", "crashed repeatedly": "attempts_exhausted", "permanent": "handler_error", "unknown type": "handler_missing", "panic": "panic"}[tc.name]
				if !strings.Contains(logs, "cause"+cause) || !strings.Contains(logs, "task.typeemail") || strings.Contains(logs, "private panic data") {
					t.Fatalf("unsafe or incomplete failure log: %s", logs)
				}
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
		r := &Reservation{Task: &Task{ID: "one", Type: "email"}, Token: "t", Attempts: 1}
		if err = worker.execute(context.Background(), r, time.Now()); err != nil || !released {
			t.Fatalf("timeout %v released=%v", err, released)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		released = false
		if err = worker.execute(ctx, r, time.Now()); !errors.Is(err, context.Canceled) || released {
			t.Fatalf("cancel %v released=%v", err, released)
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
	if err = w.execute(context.Background(), &Reservation{Task: &Task{Type: "x"}, Token: "t", Attempts: 2}, time.Now()); !errors.Is(err, failure) {
		t.Fatalf("lost failure %v", err)
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
			q, err := buildTestWorker(Definition[string]{Queue: "q", MessageType: "m", Version: 1}, store, func(context.Context, string) error { return errors.New("temporary") }, WorkerConfig{OnFailed: func(ctx context.Context, event FailureEvent) error {
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
			r := &Reservation{Task: &Task{ID: "id", Type: "m.v1", Payload: []byte(tc.payload)}, Token: "token", Attempts: tc.attempt}
			err = q.execute(context.Background(), r, time.Now())
			if !errors.Is(err, tc.archiveErr) || notified != archived || r.Task.ID != "id" {
				t.Fatalf("err=%v notified=%v archived=%v task=%s", err, notified, archived, r.Task.ID)
			}
			logs := strings.Join(obs.Logger.(*testLog).events, "\n")
			if strings.Contains(logs, "private callback") || (notified && !strings.Contains(logs, "failure.callback_failed")) {
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
			logged := strings.Contains(strings.Join(obs.Logger.(*testLog).events, "\n"), "failure.callback_failed")
			if logged != timeout {
				t.Fatalf("callback failure logged=%v timeout=%v", logged, timeout)
			}
		})
	}
}
