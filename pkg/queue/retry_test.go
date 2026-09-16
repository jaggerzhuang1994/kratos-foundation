package queue

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestRetryPolicyDefaultsAndExplicitZeroBackoff(t *testing.T) {
	defaults, err := resolveRetryPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.MaxAttempts != 3 || defaults.MinBackoff != 500*time.Millisecond || defaults.MaxBackoff != 30*time.Second {
		t.Fatalf("default retry = %#v", defaults)
	}
	explicit, err := resolveRetryPolicy(&RetryPolicy{MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.MinBackoff != 0 || explicit.MaxBackoff != 0 {
		t.Fatalf("explicit retry gained backoff: %#v", explicit)
	}
}

func TestPermanentPreservesCauseAndSupportsWrappedDetection(t *testing.T) {
	cause := errors.New("invalid payload")
	permanent := Permanent(cause)
	if permanent == nil || !errors.Is(permanent, cause) {
		t.Fatalf("Permanent() = %v", permanent)
	}
	if !IsPermanent(permanent) || !IsPermanent(fmt.Errorf("publish: %w", permanent)) {
		t.Fatal("IsPermanent() did not traverse the error chain")
	}
	if Permanent(nil) != nil || IsPermanent(cause) || IsPermanent(nil) {
		t.Fatal("nil or ordinary error was classified as permanent")
	}
}

func TestPermanentNilReceiver(t *testing.T) {
	var value *permanentError
	if value.Error() == "" || value.Unwrap() != nil {
		t.Fatal("invalid nil receiver")
	}
}
