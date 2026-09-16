package queue

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

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
			consumer, err := NewConsumer(Definition[string]{Queue: "q", MessageType: "m", Version: 1}, store, Handle(func(context.Context, string) error { return errors.New("temporary") }), ConsumerConfig{OnFailed: func(ctx context.Context, event FailureEvent) error {
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
			err = consumer.worker.execute(context.Background(), r, time.Now())
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
			worker := &Worker{onFailed: func(ctx context.Context, _ FailureEvent) error {
				if timeout {
					<-ctx.Done()
				}
				return nil
			}, log: obs.Logger, config: WorkerConfig{StorageTimeout: time.Second}}
			worker.notifyFailure(context.Background(), FailureEvent{FailedTask: FailedTask{Task: &Task{ID: "id"}}})
			logged := strings.Contains(strings.Join(obs.Logger.(*testLog).events, "\n"), "failure.callback_failed")
			if logged != timeout {
				t.Fatalf("callback failure logged=%v timeout=%v", logged, timeout)
			}
		})
	}
}
