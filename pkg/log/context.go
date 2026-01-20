package log

import "context"

type logKey struct{}

func NewContext(ctx context.Context, log Log) context.Context {
	return context.WithValue(ctx, logKey{}, log)
}

func FromContext(ctx context.Context) (log Log, ok bool) {
	log, ok = ctx.Value(logKey{}).(Log)
	return
}
