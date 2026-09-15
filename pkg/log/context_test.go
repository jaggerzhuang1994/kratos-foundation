package log

import (
	"context"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/request"
	"testing"
)

func TestDebugContextPreservesParentAndFields(t *testing.T) {
	parent := WithKv(context.Background(), "request.id", "one")
	child := request.WithDebug(parent)
	if request.IsDebug(parent) || !request.IsDebug(child) {
		t.Fatal("debug context leaked or missing")
	}
	if kvFromCtx(child)[1] != "one" {
		t.Fatal("debug lost request fields")
	}
}
