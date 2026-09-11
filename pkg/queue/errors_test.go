package queue

import (
	"errors"
	"fmt"
	"testing"
)

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
