package jobcontext

import (
	"context"
)

type nameKey struct{}

func WithJobName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, nameKey{}, name)
}

func GetJobName(ctx context.Context) string {
	name, _ := ctx.Value(nameKey{}).(string)
	return name
}
