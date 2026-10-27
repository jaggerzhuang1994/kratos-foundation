package database

import (
	"context"
	"fmt"
)

// AutoMigrate 在主库上执行 GORM AutoMigrate，供配置了 read_replicas 的连接安全迁移。
// GORM Migrator 用 SELECT 判断表和列是否存在，直接调用 Connection(ctx).AutoMigrate 时这些检查会被分发到副本，
// 副本尚未同步时会误判并重复建表。本函数等价于 Connection(UsePrimary(ctx)).AutoMigrate(models...)，
// 没有副本的连接行为不变；连接由 ctx 中的 UseConnection 选择，事务 Context 中沿用该事务。
func AutoMigrate(ctx context.Context, manager Manager, models ...any) error {
	if err := manager.Connection(UsePrimary(ctx)).AutoMigrate(models...); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}
	return nil
}
