package database

import (
	"context"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

type Manager interface {
	GetConnection(ctx context.Context) *gorm.DB
	TransactionManager
}

type manager struct {
	db         *gorm.DB
	log        log.Log
	dbResolver DbResolver
}

func NewManager(
	log log.Log,
	config Config,
	defaultConnection DefaultConnection,
	gormConfig GormConfig,
	tracingPlugin TracingPlugin,
	metricsPlugin MetricsPlugin,
	dbResolver DbResolver,
) (Manager, error) {
	db, err := gorm.Open(defaultConnection, gormConfig)
	if err != nil {
		return nil, err
	}

	// tracing plugin
	if tracingPlugin != nil {
		if err = db.Use(tracingPlugin); err != nil {
			return nil, err
		}
	}

	// metrics plugin
	if metricsPlugin != nil {
		if err = db.Use(metricsPlugin); err != nil {
			return nil, err
		}
	}

	// dbresolver
	if dbResolver != nil {
		if err = db.Use(dbResolver); err != nil {
			return nil, err
		}
	}

	return &manager{
		db,
		log.WithModule("database", config.GetLog()),
		dbResolver,
	}, nil
}

func (mgr *manager) GetConnection(ctx context.Context) *gorm.DB {
	var db *gorm.DB

	// 事务
	if tx, ok := getTx(ctx); ok {
		// 如果使用事务，则不使用 dbresolver 切换连接
		return tx
	}

	// 默认连接
	db = mgr.db.WithContext(ctx)

	// 指定数据库连接
	if conn, ok := getConnection(ctx); ok {
		db = db.Clauses(dbresolver.Use(mgr.dbResolver.ConnName(conn)))
	}

	// 指定读/写
	if operation, ok := getOperation(ctx); ok {
		db = db.Clauses(operation)
	}

	return db
}
