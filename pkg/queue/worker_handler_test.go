package queue

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func TestQueueExecutionAndOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, payload, taskType, want string
		attempt                       int
	}{
		{"valid", `"hello"`, "string.v1", "ack", 2},
		{"decode", `{`, "string.v1", "permanent", 1},
		{"validate", `""`, "string.v1", "permanent", 1},
		{"version", `"hello"`, "string.v2", "permanent", 1},
		{"exhausted", `"hello"`, "string.v1", "retry_exhausted", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obs, _ := testObservability(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			outcome, called := "", false
			store := &storeStub{
				reserve: func(context.Context, time.Time, time.Duration) (*Reservation, error) {
					if outcome != "" {
						cancel()
						return nil, context.Canceled
					}
					return &Reservation{Task: &Task{ID: "id", MessageVersion: tc.taskType, Payload: []byte(tc.payload), Headers: map[string]string{"attempt": "999"}}, Token: "token", Attempts: tc.attempt}, nil
				},
				ack: func(context.Context, *Reservation) error { outcome = "ack"; return nil },
				fail: func(_ context.Context, _ *Reservation, reason string, _ time.Time) error {
					outcome = reason
					return nil
				},
			}
			definition := Definition[string]{Queue: "mail", Version: 1, Validate: func(message string) error {
				if message == "" {
					return errors.New("empty")
				}
				return nil
			}}
			q, err := buildTestExecutionWorker(definition, store, func(_ context.Context, d Execution[string]) error {
				called = true
				if d.ID != "id" || d.Message != "hello" || d.Attempt != 2 || d.MaxAttempts != 3 || !d.CanRetry() {
					t.Errorf("delivery: %+v", d)
				}
				return nil
			}, WorkerConfig{}, obs)
			if err != nil {
				t.Fatal(err)
			}
			if err := q.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if outcome != tc.want || called != (tc.want == "ack") {
				t.Fatalf("outcome=%s called=%v", outcome, called)
			}
		})
	}
}

func TestQueueHooksAndMiddleware(t *testing.T) {
	for _, mode := range []string{"handler", "before", "cancel", "nil classifier", "permanent"} {
		t.Run(mode, func(t *testing.T) {
			obs, _ := testObservability(t)
			var order []string
			outcome := ""
			failure := errors.New("domain error")
			store := &storeStub{
				fail:    func(context.Context, *Reservation, string, time.Time) error { outcome = "fail"; return nil },
				release: func(context.Context, *Reservation, time.Time) error { outcome = "retry"; return nil },
			}
			middleware := func(name string) Middleware[string] {
				return func(next ExecutionHandler[string]) ExecutionHandler[string] {
					return func(ctx context.Context, d Execution[string]) error { order = append(order, name); return next(ctx, d) }
				}
			}
			config := WorkerConfig{BeforeHandle: func(context.Context) error {
				order = append(order, "before")
				if mode == "before" {
					return failure
				}
				if mode == "cancel" {
					return context.Canceled
				}
				return nil
			}, ClassifyError: func(err error) error {
				order = append(order, "classify")
				if mode == "nil classifier" {
					return nil
				}
				return Permanent(err)
			}}
			q, err := buildTestWorker(Definition[string]{Queue: "q", Version: 1}, store, func(context.Context, string) error {
				order = append(order, "handle")
				if mode == "permanent" {
					return Permanent(failure)
				}
				return failure
			}, config, obs, middleware("outer"), middleware("inner"))
			if err != nil {
				t.Fatal(err)
			}
			err = q.execute(context.Background(), &Reservation{Task: &Task{ID: "id", MessageVersion: "string.v1", Payload: []byte(`"hello"`)}, Token: "token", Attempts: 1}, time.Now())
			want := []string{"before", "outer", "inner", "handle", "classify"}
			if mode == "before" || mode == "cancel" {
				want = []string{"before", "classify"}
			}
			if mode == "permanent" {
				want = want[:4]
			}
			wantOutcome := "fail"
			if mode == "nil classifier" {
				wantOutcome = "retry"
			}
			if err != nil || outcome != wantOutcome || !reflect.DeepEqual(order, want) {
				t.Fatalf("err=%v outcome=%s order=%v", err, outcome, order)
			}
		})
	}
}

func TestConfiguration(t *testing.T) {
	obs, _ := testObservability(t)
	d := Definition[string]{Queue: "mail", Version: 1}
	handler := func(context.Context, string) error { return nil }
	for _, config := range []WorkerConfig{
		{Concurrency: -1}, {Timeout: -1}, {MaxAttempts: -1}, {Lease: time.Second},
		{Timeout: time.Duration(math.MaxInt64)}, {StorageTimeout: time.Duration(math.MaxInt64)},
		{MaxAttempts: 2, Retry: &RetryPolicy{MaxAttempts: 3}},
	} {
		if _, err := buildTestWorker(d, &storeStub{}, handler, config, obs); err == nil {
			t.Fatalf("accepted config: %+v", config)
		}
	}
	if _, err := buildTestWorker(Definition[string]{}, &storeStub{}, handler, WorkerConfig{}, obs); err == nil {
		t.Fatal("accepted definition")
	}
	if _, err := buildTestWorker(d, &storeStub{}, nil, WorkerConfig{}, obs); err == nil {
		t.Fatal("accepted handler")
	}
	if _, err := buildTestExecutionWorker(d, &storeStub{}, nil, WorkerConfig{}, obs); err == nil {
		t.Fatal("accepted execution handler")
	}
	for _, middleware := range []Middleware[string]{nil, func(ExecutionHandler[string]) ExecutionHandler[string] { return nil }} {
		if _, err := buildTestWorker(d, &storeStub{}, handler, WorkerConfig{}, obs, middleware); err == nil {
			t.Fatal("accepted middleware")
		}
	}
	q, err := buildTestWorker(d, &storeStub{}, handler, WorkerConfig{Timeout: 2 * time.Minute, MaxAttempts: 5}, obs)
	if err != nil {
		t.Fatal(err)
	}
	if q.config.Lease != 126*time.Second || q.retry.MaxAttempts != 5 {
		t.Fatalf("defaults: %+v", q.config)
	}
}

func TestQueueWaitTimeoutDoesNotExecuteBusiness(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs, _ := testObservability(t)
		released := false
		store := &storeStub{release: func(context.Context, *Reservation, time.Time) error { released = true; return nil }}
		q, err := buildTestWorker(Definition[string]{Queue: "q", Version: 1}, store, func(context.Context, string) error { t.Error("business ran after waiting timeout"); return nil }, WorkerConfig{
			Timeout:      time.Second,
			BeforeHandle: func(ctx context.Context) error { <-ctx.Done(); return nil },
		}, obs)
		if err != nil {
			t.Fatal(err)
		}
		err = q.execute(context.Background(), &Reservation{Task: &Task{ID: "id", MessageVersion: "string.v1", Payload: []byte(`"message"`)}, Token: "token", Attempts: 1}, time.Now())
		if err != nil || !released {
			t.Fatalf("timeout err=%v released=%v", err, released)
		}
	})
}

func TestQueueDisabledLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs, _ := testObservability(t)
		published := false
		store := &storeStub{enqueue: func(context.Context, *Task) error { published = true; return nil }}
		producer, err := NewQueue(Definition[string]{Queue: "mail", Version: 1}, store, obs)
		if err != nil {
			t.Fatal(err)
		}
		q, err := producer.Worker(func(context.Context, string) error { t.Error("disabled handler called"); return nil }, WorkerConfig{DisableProcessing: true, Timeout: 2 * time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := producer.Post(context.Background(), "still enabled"); err != nil || !published {
			t.Fatalf("processing disable affected publishing: %v", err)
		}
		done := make(chan error, 1)
		go func() { done <- q.Start(context.Background()) }()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("disabled runtime returned early")
		default:
		}
		if err := q.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestQueueSharesDefinitionAndStore(t *testing.T) {
	obs, _ := testObservability(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var task *Task
	store := &storeStub{
		enqueue: func(_ context.Context, value *Task) error { task = value; return nil },
		reserve: func(context.Context, time.Time, time.Duration) (*Reservation, error) {
			return &Reservation{Task: task, Token: "token", Attempts: 1}, nil
		},
		ack: func(context.Context, *Reservation) error { cancel(); return nil },
	}
	got := ""
	producer, err := NewQueue(Definition[string]{Queue: "mail", Version: 1}, store, obs)
	if err != nil {
		t.Fatal(err)
	}
	q, err := producer.Worker(func(_ context.Context, message string) error { got = message; return nil }, WorkerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := producer.Post(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := q.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := q.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Fatalf("message = %q", got)
	}
}

func buildTestWorker[T any](d Definition[T], store Store, handler func(context.Context, T) error, config WorkerConfig, obs Observability, middleware ...Middleware[T]) (*Worker[T], error) {
	q, err := NewQueue(d, store, obs)
	if err != nil {
		return nil, err
	}
	return q.Worker(handler, config, middleware...)
}
func buildTestExecutionWorker[T any](d Definition[T], store Store, handler ExecutionHandler[T], config WorkerConfig, obs Observability, middleware ...Middleware[T]) (*Worker[T], error) {
	q, err := NewQueue(d, store, obs)
	if err != nil {
		return nil, err
	}
	return q.WorkerWithExecution(handler, config, middleware...)
}

func TestWorkersHaveIndependentLifecycles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs, _ := testObservability(t)
		q, err := NewQueue(Definition[string]{Queue: "mail", Version: 1}, &storeStub{}, obs)
		if err != nil {
			t.Fatal(err)
		}
		handler := func(context.Context, string) error { return nil }
		first, err := q.Worker(handler, WorkerConfig{DisableProcessing: true})
		if err != nil {
			t.Fatal(err)
		}
		second, err := q.Worker(handler, WorkerConfig{DisableProcessing: true})
		if err != nil {
			t.Fatal(err)
		}
		firstDone, secondDone := make(chan error, 1), make(chan error, 1)
		go func() { firstDone <- first.Start(context.Background()) }()
		go func() { secondDone <- second.Start(context.Background()) }()
		synctest.Wait()
		if err := first.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := <-firstDone; err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-secondDone:
			t.Fatalf("stopping first stopped second: %v", err)
		default:
		}
		if err := second.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := <-secondDone; err != nil {
			t.Fatal(err)
		}
	})
}

func TestExecutionRetryBudget(t *testing.T) {
	for _, tc := range []struct {
		attempt, max int
		want         bool
	}{{0, 3, false}, {1, 3, true}, {3, 3, false}, {4, 3, false}} {
		if got := (Execution[string]{Attempt: tc.attempt, MaxAttempts: tc.max}).CanRetry(); got != tc.want {
			t.Fatalf("retry %v = %v", tc, got)
		}
	}
}
