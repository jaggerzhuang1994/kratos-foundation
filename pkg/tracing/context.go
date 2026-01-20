package tracing

import "context"

type tracingKey struct{}

func NewContext(ctx context.Context, tracing Tracing) context.Context {
	return context.WithValue(ctx, tracingKey{}, tracing)
}

func FromContext(ctx context.Context) (tracing Tracing, ok bool) {
	tracing, ok = ctx.Value(tracingKey{}).(Tracing)
	return
}
