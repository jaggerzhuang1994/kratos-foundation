package kafka

import (
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
