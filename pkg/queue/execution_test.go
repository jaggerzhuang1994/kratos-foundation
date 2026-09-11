package queue

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestExecutionPersistsOutcome(t *testing.T) {
	for _, tc := range []struct {
		name    string
		attempt int
		handler Handler
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
			handlers := map[string]Handler{"other": func(context.Context, *Task) error { return nil }}
			if tc.handler != nil {
				handlers["email"] = tc.handler
			}
			worker, err := NewWorker(WorkerConfig{Name: "test", Queue: "mail"}, store, handlers, obs)
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
		worker, err := NewWorker(WorkerConfig{Name: "test", Queue: "mail", Timeout: time.Second}, store, map[string]Handler{"email": func(ctx context.Context, _ *Task) error { <-ctx.Done(); return nil }}, obs)
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
	w, err := NewWorker(WorkerConfig{Name: "test", Queue: "mail"}, store, map[string]Handler{"x": func(context.Context, *Task) error { return errors.New("retry") }}, obs)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.execute(context.Background(), &Reservation{Task: &Task{Type: "x"}, Token: "t", Attempts: 2}, time.Now()); !errors.Is(err, failure) {
		t.Fatalf("lost failure %v", err)
	}
}
