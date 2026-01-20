package app_info

import (
	"context"
)

// appInfoKey 上下文中存储 AppInfo 的键类型
type appInfoKey struct{}

// NewContext 创建包含应用信息的新上下文
// 将 AppInfo 实例存储到上下文中，以便在调用链中传递
func NewContext(ctx context.Context, ai AppInfo) context.Context {
	return context.WithValue(ctx, appInfoKey{}, ai)
}

// FromContext 从上下文中获取应用信息
// 返回 AppInfo 实例和是否存在的标志
func FromContext(ctx context.Context) (ai AppInfo, ok bool) {
	ai, ok = ctx.Value(appInfoKey{}).(AppInfo)
	return
}
