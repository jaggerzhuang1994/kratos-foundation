package queue

import (
	"context"
	"testing"
)

func TestDeliveryAdapters(t *testing.T) {
	for _, tc := range []struct {
		attempt, max int
		want         bool
	}{{0, 3, false}, {1, 3, true}, {3, 3, false}, {4, 3, false}} {
		if got := (Delivery[string]{Attempt: tc.attempt, MaxAttempts: tc.max}).CanRetry(); got != tc.want {
			t.Fatalf("retry %v = %v", tc, got)
		}
	}
	if Handle[string](nil) != nil || HandleDelivery[string](nil) != nil {
		t.Fatal("nil handler hidden")
	}
	got := ""
	handler := Handle(func(_ context.Context, message string) error { got = message; return nil })
	if err := handler(context.Background(), Delivery[string]{Message: "hello"}); err != nil || got != "hello" {
		t.Fatalf("adapter %q %v", got, err)
	}
}
