package lock_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/lock"
)

func TestLeaseSentinelErrorsKeepDistinctIdentities(t *testing.T) {
	if errors.Is(lock.ErrNotAcquired, lock.ErrNotHeld) || errors.Is(lock.ErrNotHeld, lock.ErrNotAcquired) {
		t.Fatal("lock acquisition and ownership failures share an identity")
	}
	if !errors.Is(fmt.Errorf("try lock: %w", lock.ErrNotAcquired), lock.ErrNotAcquired) {
		t.Fatal("ErrNotAcquired cannot be matched through wrapping")
	}
	if !errors.Is(fmt.Errorf("unlock: %w", lock.ErrNotHeld), lock.ErrNotHeld) {
		t.Fatal("ErrNotHeld cannot be matched through wrapping")
	}
}
