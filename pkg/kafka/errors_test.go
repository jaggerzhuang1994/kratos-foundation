package kafka

import (
	"errors"
	"fmt"
	"strings"
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

func TestBatchErrorReportsAndUnwrapsEveryFailure(t *testing.T) {
	first := errors.New("first")
	second := errors.New("second")
	err := &BatchError{Failures: []BatchFailure{
		{Index: 0, MessageID: "one", Err: first},
		{Index: 1, MessageID: "two"},
		{Index: 2, MessageID: "three", Err: second},
	}}
	if !strings.Contains(err.Error(), "3 message(s)") {
		t.Fatalf("Error() = %q", err.Error())
	}
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("BatchError did not expose causes: %v", err)
	}

	var nilBatch *BatchError
	if nilBatch.Error() == "" || nilBatch.Unwrap() != nil {
		t.Fatalf("nil BatchError behavior = %q, %#v", nilBatch.Error(), nilBatch.Unwrap())
	}
}

func TestBatchErrorDescribesFailureCountAndNilReceiver(t *testing.T) {
	var nilBatchError *BatchError
	if got := nilBatchError.Error(); got != "queue batch publish failed" {
		t.Fatalf("nil BatchError.Error() = %q", got)
	}

	err := &BatchError{Failures: []BatchFailure{
		{Index: 0, MessageID: "message-1", Err: errors.New("first failure")},
		{Index: 1, MessageID: "message-2", Err: errors.New("second failure")},
	}}
	if got := err.Error(); got != "queue batch publish failed for 2 message(s)" {
		t.Fatalf("BatchError.Error() = %q", got)
	}
}

func TestBatchErrorUnwrapExposesOnlyNonNilFailureCauses(t *testing.T) {
	var nilBatchError *BatchError
	if got := nilBatchError.Unwrap(); got != nil {
		t.Fatalf("nil BatchError.Unwrap() = %#v, want nil", got)
	}

	first := errors.New("first failure")
	second := errors.New("second failure")
	err := &BatchError{Failures: []BatchFailure{
		{Index: 0, MessageID: "message-1", Err: first},
		{Index: 1, MessageID: "message-2"},
		{Index: 2, MessageID: "message-3", Err: second},
	}}

	causes := err.Unwrap()
	if len(causes) != 2 || causes[0] != first || causes[1] != second {
		t.Fatalf("BatchError.Unwrap() = %#v, want [%v %v]", causes, first, second)
	}
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("errors.Is did not traverse all batch failures: %v", err)
	}
}
