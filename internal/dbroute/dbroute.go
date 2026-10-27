// Package dbroute 保存数据库读路由的 Context 标记，供 pkg/database 与队列仓储共享而不形成导入环。
package dbroute

import "context"

type primaryKey struct{}

// WithPrimary 标记后续查询必须走主库。
func WithPrimary(ctx context.Context) context.Context {
	return context.WithValue(ctx, primaryKey{}, true)
}

// Primary 报告 Context 是否要求查询走主库。
func Primary(ctx context.Context) bool {
	primary, _ := ctx.Value(primaryKey{}).(bool)
	return primary
}
