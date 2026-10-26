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

//go:noinline
func createStackError() *Error {
	return New(500, "INTERNAL", "internal").WithErrStack()
}

// WithErrStack 的首帧是业务调用点，栈中不含错误包或日志包的内部帧。
func TestWithErrStackStartsAtCallSiteWithoutInternalFrames(t *testing.T) {
	stack := createStackError().ErrStack()
	lines := strings.Split(strings.TrimSpace(stack), "\n")
	if len(lines) < 2 || !strings.HasSuffix(lines[0], "pkg/errors.createStackError") {
		t.Fatalf("first frame = %q, want createStackError\n%s", lines[0], stack)
	}
	for _, frame := range lines {
		if strings.Contains(frame, "pkg/errors.callers") || strings.Contains(frame, "(*Error).WithErrStack") || strings.Contains(frame, "pkg/log.") {
			t.Fatalf("stack contains internal frame %q\n%s", frame, stack)
		}
	}
}

//go:noinline
func wrapWithOuterStack(cause error) *Error {
	return New(500, "OUTER", "outer").WithErrStack().WithCause(cause)
}

// 多层包装只保留各层显式采集的栈，每段栈恰好出现一次；格式化不会追加新的调用栈。
func TestNestedErrStackContainsEachCaptureOnce(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	inner := createStackError()
	middle := New(503, "DEPENDENCY", "dependency").WithCause(fmt.Errorf("call inner: %w", inner))
	outer := wrapWithOuterStack(middle)
	for _, text := range []string{
		outer.ErrStack(),
		// 重复格式化与再次包装不重新采集栈。
		fmt.Sprintf("%+v", New(500, "WRAP", "wrap").WithCause(outer)),
		FromError(outer.GRPCStatus().Err()).ErrStack(),
	} {
		for _, capture := range []string{"pkg/errors.createStackError", "pkg/errors.wrapWithOuterStack"} {
			if got := strings.Count(text, capture); got != 1 {
				t.Fatalf("%s appears %d times\n%s", capture, got, text)
			}
		}
	}
}
