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

func TestConsumerDeliveryAndOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, payload, taskType, want string
		attempt                       int
	}{
		{"valid", `"hello"`, "message.v1", "ack", 2},
		{"decode", `{`, "message.v1", "permanent", 1},
		{"validate", `""`, "message.v1", "permanent", 1},
		{"version", `"hello"`, "message.v2", "permanent", 1},
		{"exhausted", `"hello"`, "message.v1", "retry_exhausted", 4},
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
					return &Reservation{Task: &Task{ID: "id", Type: tc.taskType, Payload: []byte(tc.payload), Headers: map[string]string{"attempt": "999"}}, Token: "token", Attempts: tc.attempt}, nil
				},
				ack: func(context.Context, *Reservation) error { outcome = "ack"; return nil },
				fail: func(_ context.Context, _ *Reservation, reason string, _ time.Time) error {
					outcome = reason
					return nil
				},
			}
			definition := Definition[string]{Queue: "mail", MessageType: "message", Version: 1, Validate: func(message string) error {
				if message == "" {
					return errors.New("empty")
				}
				return nil
			}}
			consumer, err := NewConsumer(definition, store, HandleDelivery(func(_ context.Context, d Delivery[string]) error {
				called = true
				if d.ID != "id" || d.Message != "hello" || d.Attempt != 2 || d.MaxAttempts != 3 || !d.CanRetry() {
					t.Errorf("delivery: %+v", d)
				}
				return nil
			}), ConsumerConfig{}, obs)
			if err != nil {
				t.Fatal(err)
			}
			if err := consumer.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if outcome != tc.want || called != (tc.want == "ack") {
				t.Fatalf("outcome=%s called=%v", outcome, called)
			}
		})
	}
}

func TestConsumerHooksAndMiddleware(t *testing.T) {
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
				return func(next DeliveryHandler[string]) DeliveryHandler[string] {
					return func(ctx context.Context, d Delivery[string]) error { order = append(order, name); return next(ctx, d) }
				}
			}
			config := ConsumerConfig{Before: func(context.Context) error {
				order = append(order, "before")
				if mode == "before" {
					return failure
				}
				if mode == "cancel" {
					return context.Canceled
				}
				return nil
			}, Classify: func(err error) error {
				order = append(order, "classify")
				if mode == "nil classifier" {
					return nil
				}
				return Permanent(err)
			}}
			consumer, err := NewConsumer(Definition[string]{Queue: "q", MessageType: "m", Version: 1}, store, Handle(func(context.Context, string) error {
				order = append(order, "handle")
				if mode == "permanent" {
					return Permanent(failure)
				}
				return failure
			}), config, obs, middleware("outer"), middleware("inner"))
			if err != nil {
				t.Fatal(err)
			}
			err = consumer.worker.execute(context.Background(), &Reservation{Task: &Task{ID: "id", Type: "m.v1", Payload: []byte(`"hello"`)}, Token: "token", Attempts: 1}, time.Now())
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

func TestConsumerConfiguration(t *testing.T) {
	obs, _ := testObservability(t)
	d := Definition[string]{Queue: "mail", MessageType: "message", Version: 1}
	handler := Handle(func(context.Context, string) error { return nil })
	for _, config := range []ConsumerConfig{
		{Concurrency: -1}, {Timeout: -1}, {MaxAttempts: -1}, {Lease: time.Second},
		{Timeout: time.Duration(math.MaxInt64)}, {StorageTimeout: time.Duration(math.MaxInt64)},
		{MaxAttempts: 2, Retry: &RetryPolicy{MaxAttempts: 3}},
	} {
		if _, err := NewConsumer(d, &storeStub{}, handler, config, obs); err == nil {
			t.Fatalf("accepted config: %+v", config)
		}
	}
	if _, err := NewConsumer(Definition[string]{}, &storeStub{}, handler, ConsumerConfig{}, obs); err == nil {
		t.Fatal("accepted definition")
	}
	if _, err := NewConsumer(d, &storeStub{}, nil, ConsumerConfig{}, obs); err == nil {
		t.Fatal("accepted handler")
	}
	for _, middleware := range []Middleware[string]{nil, func(DeliveryHandler[string]) DeliveryHandler[string] { return nil }} {
		if _, err := NewConsumer(d, &storeStub{}, handler, ConsumerConfig{}, obs, middleware); err == nil {
			t.Fatal("accepted middleware")
		}
	}
	consumer, err := NewConsumer(d, &storeStub{}, handler, ConsumerConfig{Timeout: 2 * time.Minute, MaxAttempts: 5}, obs)
	if err != nil {
		t.Fatal(err)
	}
	if consumer.worker.config.Lease != 126*time.Second || consumer.worker.retry.MaxAttempts != 5 {
		t.Fatalf("defaults: %+v", consumer.worker.config)
	}
}

func TestConsumerWaitTimeoutDoesNotExecuteBusiness(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs, _ := testObservability(t)
		released := false
		store := &storeStub{release: func(context.Context, *Reservation, time.Time) error { released = true; return nil }}
		consumer, err := NewConsumer(Definition[string]{Queue: "q", MessageType: "m", Version: 1}, store, Handle(func(context.Context, string) error { t.Error("business ran after waiting timeout"); return nil }), ConsumerConfig{
			Timeout: time.Second,
			Before:  func(ctx context.Context) error { <-ctx.Done(); return nil },
		}, obs)
		if err != nil {
			t.Fatal(err)
		}
		err = consumer.worker.execute(context.Background(), &Reservation{Task: &Task{ID: "id", Type: "m.v1", Payload: []byte(`"message"`)}, Token: "token", Attempts: 1}, time.Now())
		if err != nil || !released {
			t.Fatalf("timeout err=%v released=%v", err, released)
		}
	})
}

func TestConsumerDisabledLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs, _ := testObservability(t)
		consumer, err := NewConsumer(Definition[string]{Queue: "mail", MessageType: "message", Version: 1}, &storeStub{}, Handle(func(context.Context, string) error { t.Error("disabled handler called"); return nil }), ConsumerConfig{Disabled: true, Timeout: 2 * time.Minute}, obs)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- consumer.Start(context.Background()) }()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("disabled runtime returned early")
		default:
		}
		if err := consumer.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}
