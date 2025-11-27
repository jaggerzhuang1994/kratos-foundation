package errors

import (
	"testing"

	"github.com/pkg/errors"
)

func TestErrStack(t *testing.T) {
	errStack := New(0, "", "").WithErrStack()
	t.Log(errStack.ErrStack())
}

func TestErr(t *testing.T) {
	err := New(1, "reason", "message").
		WithErrStack().
		WithMetadata(map[string]string{
			"key": "value",
		}).
		WithCause(errors.New("cause")).
		WithHttpData(map[string]any{
			"user": "user_info",
		}).
		WithHttpHeaders(map[string][]string{
			"a": {"b"},
		}).
		WithHttpResponse("http response").
		WithReasonCode(200001)
	t.Logf("%+v", err)
}
