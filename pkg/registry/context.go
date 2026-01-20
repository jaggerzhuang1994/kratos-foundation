package registry

import (
	"context"
)

type registryCtxKey struct{}

func NewContext(ctx context.Context, registry Registrar) context.Context {
	if registry == nil {
		return ctx
	}
	return context.WithValue(ctx, registryCtxKey{}, registry)
}

func FromContext(ctx context.Context) (r Registrar, ok bool) {
	r, ok = ctx.Value(registryCtxKey{}).(Registrar)
	return
}
