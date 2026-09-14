package log

import "context"

type contextKVKey struct{}

func kvFromContext(ctx context.Context) []any {
	kv, _ := ctx.Value(contextKVKey{}).([]any)
	return kv
}

// WithKv 向 Context 追加日志字段，并保留父 Context 中已有的字段。
// module 不参与日志归属，防止请求上下文改写组件模块。
func WithKv(ctx context.Context, kv ...any) context.Context {
	prefix := kvFromContext(ctx)
	values := make([]any, 0, len(prefix)+len(kv))
	values = append(values, prefix...)
	values = append(values, kv...)
	return context.WithValue(ctx, contextKVKey{}, values)
}

// kvFromCtx 返回 Context 日志字段的独立副本。
func kvFromCtx(ctx context.Context) []any {
	return append([]any(nil), kvFromContext(ctx)...)
}
