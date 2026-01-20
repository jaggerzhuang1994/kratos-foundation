package discovery

import (
	"context"
)

type discoveryCtxKey struct{}

func NewContext(ctx context.Context, discovery Discovery) context.Context {
	if discovery == nil {
		return ctx
	}
	return context.WithValue(ctx, discoveryCtxKey{}, discovery)
}

func FromContext(ctx context.Context) (r Discovery, ok bool) {
	r, ok = ctx.Value(discoveryCtxKey{}).(Discovery)
	return
}
