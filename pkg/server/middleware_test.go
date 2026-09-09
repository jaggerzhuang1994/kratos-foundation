package server

import (
	"context"
	"github.com/go-kratos/kratos/v2/middleware"
	"strings"
	"testing"
)

func marker(name string, calls *[]string) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			*calls = append(*calls, name)
			return next(ctx, req)
		}
	}
}

func TestMiddlewareSetBuildsStablePriorityOrderAndNamedReplacement(t *testing.T) {
	var calls []string
	base := middlewareSet{
		{Name: "low", Priority: 10, Middleware: marker("low", &calls)},
		{Name: "replace", Priority: 20, Middleware: marker("old", &calls)},
	}
	built := base.build([]MiddlewareSpec{
		{Name: "replace", Priority: 5, Middleware: marker("new", &calls)},
		{Priority: 10, Middleware: marker("equal", &calls)},
		{Name: "remove", Middleware: nil},
	})
	h := func(context.Context, any) (any, error) { calls = append(calls, "handler"); return nil, nil }
	for i := len(built) - 1; i >= 0; i-- {
		h = built[i](h)
	}
	if _, err := h(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(calls, ","), "new,low,equal,handler"; got != want {
		t.Fatalf("order=%s want=%s", got, want)
	}
}
