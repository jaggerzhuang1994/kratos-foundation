package gorm

import (
	"context"
	"errors"
	"fmt"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"gorm.io/gorm"
)

// SimpleConfig 为只保存队列数据的模式指定独立物理表，不支持运行时换表。
type SimpleConfig struct {
	Table string `json:"table"`
	// RetainCompleted 保留成功任务，默认 false；与 Config 中同名字段语义一致。
	RetainCompleted bool `json:"retain_completed"`
}

type simpleModel struct{ Model }

func (*simpleModel) TableName() string { return "queue_tasks" }

// SimpleRepo 使用框架模型和工厂，保留 Repo 的事务、租约及失败管理能力。
// 一个实例对应一张独立表；不拥有连接，构造时不自动迁移。
type SimpleRepo struct{ *Repo[*simpleModel] }

// NewSimpleRepo 校验表名和方言，业务无需定义 Model 或 Factory。
func NewSimpleRepo(ctx context.Context, provider ConnectionProvider, config SimpleConfig) (*SimpleRepo, error) {
	if config.Table == "" {
		return nil, errors.New("queue simple table is required")
	}
	repo, err := newRepo(ctx, provider, func(context.Context, *databasequeue.TaskRecord) (*simpleModel, error) { return &simpleModel{}, nil }, config.Table, Config{RetainCompleted: config.RetainCompleted})
	if err != nil {
		return nil, err
	}
	return &SimpleRepo{Repo: repo}, nil
}

// Migrate 显式创建或补齐简单队列表和索引，不在构造或消费时调用。
// 应仅由部署迁移命令在审核后执行；生产库的 DDL 权限、锁等待与版本管理由部署层负责。
func (r *SimpleRepo) Migrate(ctx context.Context) error {
	db := r.consumerDB(ctx)
	if db.Error != nil {
		return db.Error
	}
	if err := db.AutoMigrate(&simpleModel{}); err != nil {
		return fmt.Errorf("migrate queue simple table: %w", err)
	}
	// 兼容旧表：Failed 和租约列保留，迁移时补齐可查询状态；已完成行不回退。
	// 部署期间须停用旧 Worker，避免其忽略 completed 状态重新领取。
	if err := db.Where("status <> ?", StatusCompleted).Update("status", gorm.Expr("CASE WHEN failed = ? THEN ? WHEN reserved_until <> 0 THEN ? ELSE ? END", true, StatusFailed, StatusRunning, StatusPending)).Error; err != nil {
		return fmt.Errorf("backfill queue task status: %w", err)
	}
	return nil
}
