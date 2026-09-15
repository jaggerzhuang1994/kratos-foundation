package request

import (
	"context"
	"testing"
)

type testKey struct{}

func TestDebugIsRequestScopedAndPreservesCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	child := WithDebug(parent)
	if IsDebug(parent) || !IsDebug(child) {
		t.Fatal("debug state leaked or missing")
	}
	if !IsDebug(context.WithValue(child, testKey{}, "value")) {
		t.Fatal("child lost request state")
	}
	cancel()
	if child.Err() != context.Canceled {
		t.Fatal("debug context lost cancellation")
	}
}
