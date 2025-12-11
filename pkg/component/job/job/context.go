package job

import (
	"context"
)

type nameKey struct{}

func WithName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, nameKey{}, name)
}

func GetName(ctx context.Context) string {
	name, _ := ctx.Value(nameKey{}).(string)
	return name
}
