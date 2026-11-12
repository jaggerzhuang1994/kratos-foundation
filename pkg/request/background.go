package request

import "context"

type backgroundKey struct{}

// WithBackground 标记后台周期性调用（定时刷新缓存、轮询上游等）。
// 这类调用成功时的访问摘要降为 Debug，避免固定频率的调用淹没业务日志；失败照常记录。
// 标记随客户端调用传给下游，下游对同一次调用的访问摘要同样降级。不修改父上下文，不改变取消、超时或业务规则。
func WithBackground(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundKey{}, true)
}

// IsBackground 报告当前调用是否被标记为后台周期性调用；未标记时返回 false。
func IsBackground(ctx context.Context) bool {
	background, _ := ctx.Value(backgroundKey{}).(bool)
	return background
}
