package gorm

import (
	"context"
	"errors"
	"fmt"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
)

// SimpleConfig 为只保存队列数据的模式指定独立物理表，不支持运行时换表。
type SimpleConfig struct{ Table string }

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
	repo, err := newRepo(ctx, provider, func(context.Context, *databasequeue.TaskRecord) (*simpleModel, error) { return &simpleModel{}, nil }, config.Table)
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
	return nil
}
