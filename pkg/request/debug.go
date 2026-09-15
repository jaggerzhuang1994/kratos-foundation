// Package request 定义应用级请求状态，不依赖日志、传输或应用组装。
package request

import "context"

type debugKey struct{}

// WithDebug 返回启用诊断模式的请求上下文；入口应先完成授权。
// 不修改父上下文，不改变取消、超时或业务规则。
func WithDebug(ctx context.Context) context.Context {
	return context.WithValue(ctx, debugKey{}, true)
}

// IsDebug 报告当前请求是否处于诊断模式；未标记时返回 false。
func IsDebug(ctx context.Context) bool {
	enabled, _ := ctx.Value(debugKey{}).(bool)
	return enabled
}
