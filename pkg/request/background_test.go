package request

import (
	"context"
	"testing"
)

func TestBackgroundMarkIsScopedToDerivedContext(t *testing.T) {
	parent := context.Background()
	marked := WithBackground(parent)
	if IsBackground(parent) || !IsBackground(marked) {
		t.Fatalf("parent=%v marked=%v", IsBackground(parent), IsBackground(marked))
	}
	derived, cancel := context.WithCancel(marked)
	defer cancel()
	if !IsBackground(derived) || IsDebug(marked) {
		t.Fatal("background mark must follow derived contexts and stay independent of debug")
	}
}
