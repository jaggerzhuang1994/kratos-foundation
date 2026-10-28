package database

import (
	"context"
	"fmt"
)

// PrimaryCheck 返回检查主库可用性的就绪检查函数，可直接作为 server.ReadinessCheck 的 Check：
//
//	spec.Health().Checks(server.ReadinessCheck{Name: "database", Check: database.PrimaryCheck(manager, "")})
//
// connection 为空时检查默认连接，否则检查同名的具名连接。每次检查在主库执行 SELECT 1，
// 配置了只读副本时不访问副本；超时由调用方 Context 决定（就绪检查默认一轮 1 秒）。
// 就绪失败会让实例停止接收流量（K8s 摘除、Consul 服务发现过滤），只在主库不可用时整个服务都无法工作时注册。
func PrimaryCheck(manager Manager, connection string) func(context.Context) error {
	return func(ctx context.Context) error {
		if connection != "" {
			ctx = UseConnection(ctx, connection)
		}
		if err := manager.Connection(UsePrimary(ctx)).Exec("SELECT 1").Error; err != nil {
			return fmt.Errorf("database primary check: %w", err)
		}
		return nil
	}
}
