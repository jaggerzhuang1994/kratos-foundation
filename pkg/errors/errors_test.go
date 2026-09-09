package errors

import (
	stderrors "errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	kratoserrors "github.com/go-kratos/kratos/v2/errors"
)

func TestErrorMatchingCauseFormattingAndHelpers(t *testing.T) {
	cause := stderrors.New("storage unavailable")
	err := New(503, "UNAVAILABLE", "try later").
		WithCause(cause).
		WithReasonCode(7001).
		WithErrStack(1)

	if !stderrors.Is(err, cause) {
		t.Fatal("errors.Is did not reach cause")
	}
	if !stderrors.Is(err, New(503, "UNAVAILABLE", "different")) {
		t.Fatal("Error.Is did not match package status identity")
	}
	if !stderrors.Is(err, kratoserrors.New(503, "UNAVAILABLE", "different")) {
		t.Fatal("Error.Is did not match Kratos status identity")
	}
	if stderrors.Is(err, New(500, "UNAVAILABLE", "different")) {
		t.Fatal("Error.Is ignored status code")
	}
	if got := fmt.Sprintf("%s", err); !strings.Contains(got, "reason_code=7001") || strings.Contains(got, "storage unavailable") {
		t.Fatalf("compact format = %q", got)
	}
	if got := fmt.Sprintf("%q", err); !strings.HasPrefix(got, `"error:`) {
		t.Fatalf("quoted format = %q", got)
	}
	if got := fmt.Sprintf("%+v", err); !strings.Contains(got, "storage unavailable") || !strings.Contains(got, "errors_test.go") {
		t.Fatalf("verbose format does not contain cause and stack: %q", got)
	}

	if Code(err) != 503 || Reason(err) != "UNAVAILABLE" || Message(err) != "try later" || ReasonCode(err) != 7001 {
		t.Fatalf("helper projection = (%d, %q, %q, %d)", Code(err), Reason(err), Message(err), ReasonCode(err))
	}
	if ErrStack(err) == "" || HTTPData(err) != nil || len(HTTPHeaders(err)) != 0 {
		t.Fatal("helper projection lost stack or invented HTTP payload")
	}
	if Code(nil) != http.StatusOK || ReasonCode(nil) != http.StatusOK || Message(nil) != "" || ErrStack(nil) != "" || HTTPData(nil) != nil || len(HTTPHeaders(nil)) != 0 {
		t.Fatal("nil helper defaults changed")
	}
}
