package queue

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"
)

func TestWorkerLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs, _ := testObservability(t)
		store := &storeStub{reserve: func(context.Context, time.Time, time.Duration) (*Reservation, error) { return nil, nil }}
		w, err := NewWorker(WorkerConfig{Name: "worker", Queue: "mail", Concurrency: 2}, store, map[string]Handler{"x": func(context.Context, *Task) error { return nil }}, obs)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- w.Start(context.Background()) }()
		synctest.Wait()
		if err = w.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err = <-done; err != nil {
			t.Fatal(err)
		}
		if err = w.Start(context.Background()); err == nil {
			t.Fatal("restarted worker")
		}
		if err = w.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		w, err = NewWorker(WorkerConfig{Name: "worker", Queue: "mail"}, store, map[string]Handler{"x": func(context.Context, *Task) error { return nil }}, obs)
		if err != nil {
			t.Fatal(err)
		}
		if err = w.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err = w.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestWorkerDoesNotSwallowStorageErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"failure", errors.New("offline")}, {"self cancellation", context.Canceled}, {"self timeout", context.DeadlineExceeded}, {"joined lease", errors.Join(ErrLeaseLost, errors.New("offline"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obs, _ := testObservability(t)
			store := &storeStub{reserve: func(context.Context, time.Time, time.Duration) (*Reservation, error) { return nil, tc.err }}
			w, err := NewWorker(WorkerConfig{Name: "w", Queue: "q"}, store, map[string]Handler{"x": func(context.Context, *Task) error { return nil }}, obs)
			if err != nil {
				t.Fatal(err)
			}
			if err = w.Start(context.Background()); !errors.Is(err, tc.err) {
				t.Fatalf("lost error %v", err)
			}
		})
	}
}

func TestWorkerLeaseConflictAndStoredOutcome(t *testing.T) {
	for _, joined := range []bool{false, true} {
		t.Run(fmt.Sprint(joined), func(t *testing.T) {
			obs, _ := testObservability(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			count := 0
			failure := errors.New("offline")
			store := &storeStub{reserve: func(context.Context, time.Time, time.Duration) (*Reservation, error) {
				count++
				if count > 1 {
					cancel()
					return nil, context.Canceled
				}
				return &Reservation{Task: &Task{Type: "x"}, Token: "t", Attempts: 1}, nil
			}, ack: func(context.Context, *Reservation) error {
				if joined {
					return errors.Join(ErrLeaseLost, failure)
				}
				return ErrLeaseLost
			}}
			w, err := NewWorker(WorkerConfig{Name: "w", Queue: "q"}, store, map[string]Handler{"x": func(context.Context, *Task) error { return nil }}, obs)
			if err != nil {
				t.Fatal(err)
			}
			err = w.Start(ctx)
			if joined && !errors.Is(err, failure) {
				t.Fatalf("lost joined error %v", err)
			}
			if !joined && (err != nil || count != 2) {
				t.Fatalf("did not recover %d %v", count, err)
			}
		})
	}
}

func TestWorkerStopDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs, _ := testObservability(t)
		entered := make(chan struct{})
		release := make(chan struct{})
		store := &storeStub{reserve: func(context.Context, time.Time, time.Duration) (*Reservation, error) {
			return &Reservation{Task: &Task{Type: "x"}, Token: "t", Attempts: 1}, nil
		}}
		w, err := NewWorker(WorkerConfig{Name: "w", Queue: "q"}, store, map[string]Handler{"x": func(context.Context, *Task) error { close(entered); <-release; return nil }}, obs)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- w.Start(context.Background()) }()
		<-entered
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err = w.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("stop %v", err)
		}
		close(release)
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestWorkerConfigurationAndErrorClassification(t *testing.T) {
	obs, _ := testObservability(t)
	for _, tc := range []struct {
		name   string
		config WorkerConfig
	}{
		{"empty", WorkerConfig{}}, {"concurrency", WorkerConfig{Name: "w", Queue: "q", Concurrency: -1}}, {"window", WorkerConfig{Name: "w", Queue: "q", Lease: time.Second}}, {"retry", WorkerConfig{Name: "w", Queue: "q", Retry: &RetryPolicy{MaxAttempts: -1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewWorker(tc.config, nil, map[string]Handler{"x": func(context.Context, *Task) error { return nil }}, obs); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
	for _, handlers := range []map[string]Handler{nil, {"x": nil}, {" ": func(context.Context, *Task) error { return nil }}} {
		if _, err := NewWorker(WorkerConfig{Name: "w", Queue: "q"}, nil, handlers, obs); err == nil {
			t.Fatal("accepted handlers")
		}
	}
	if !errorOnly(fmt.Errorf("wrapped: %w", errors.Join(context.Canceled, context.Canceled)), context.Canceled) || errorOnly(errors.Join(context.Canceled, errors.New("failure")), context.Canceled) {
		t.Fatal("wrong classification")
	}
}
