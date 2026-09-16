package queue

import (
	"context"
	"testing"
	"time"
)

func TestEndpointSharesDefinitionAndStore(t *testing.T) {
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
	endpoint, err := NewEndpoint(Definition[string]{Queue: "mail", MessageType: "message", Version: 1}, store, Handle(func(_ context.Context, message string) error { got = message; return nil }), ConsumerConfig{}, obs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := endpoint.Publish(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := endpoint.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := endpoint.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Fatalf("message = %q", got)
	}
	if _, err := NewEndpoint(Definition[string]{}, store, Handle(func(context.Context, string) error { return nil }), ConsumerConfig{}, obs); err == nil {
		t.Fatal("accepted invalid definition")
	}
	if _, err := NewEndpoint(Definition[string]{Queue: "q", MessageType: "m", Version: 1}, store, Handle(func(context.Context, string) error { return nil }), ConsumerConfig{Concurrency: -1}, obs); err == nil {
		t.Fatal("accepted invalid consumer config")
	}
}
