package database

import (
	"context"
	"database/sql"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"gorm.io/plugin/dbresolver"

	"gorm.io/gorm"
)

type Manager struct {
	db  *gorm.DB
	log *log.Helper
}

func NewManager(
	cfg *Config,
	log *log.Log,
	gormConfig *gorm.Config,
	tracingPlugin TracingPlugin,
	defaultConnection DefaultConnection,
	dbResolver DbResolver,
) (*Manager, error) {
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

	// todo metrics

	// dbresolver
	if dbResolver != nil {
		if err = db.Use(dbResolver); err != nil {
			return nil, err
		}
	}

	return &Manager{
		db,
		log.WithModule("database", cfg.GetLog()).NewHelper(),
	}, nil
}

type useConnKey struct{}

func useConn(ctx context.Context, conn *gorm.DB) context.Context {
	return context.WithValue(ctx, useConnKey{}, conn)
}

func (mgr *Manager) GetConnection(ctx context.Context) *gorm.DB {
	tx, ok := ctx.Value(useConnKey{}).(*gorm.DB)
	if !ok {
		tx = mgr.db.WithContext(ctx)
	}

	// 指定数据库连接
	if useCon, ok := ctx.Value(useConnectionKey{}).(string); ok {
		tx = tx.Clauses(dbresolver.Use(connName(useCon)))
	}

	// 指定读/写
	if operation, ok := ctx.Value(useOperationKey{}).(dbresolver.Operation); ok {
		tx = tx.Clauses(operation)
	}

	return tx
}

type txOptions struct {
	sqlTxOptions []*sql.TxOptions
}

type TransactionOption func(o *txOptions)

func WithSqlTxOptions(opts ...*sql.TxOptions) TransactionOption {
	return func(o *txOptions) {
		o.sqlTxOptions = append(o.sqlTxOptions, opts...)
	}
}

func (mgr *Manager) Transaction(ctx context.Context, fc func(ctx2 context.Context) (err error), opts ...TransactionOption) error {
	opt := &txOptions{}
	for _, fn := range opts {
		fn(opt)
	}
	return mgr.GetConnection(ctx).Transaction(func(tx *gorm.DB) error {
		return fc(useConn(ctx, tx))
	}, opt.sqlTxOptions...)
}

type Exchange string

const (
	Binance Exchange = "binance"
)

type MarketType string

const (
	Spot MarketType = "spot"
	Swap MarketType = "swap"
)

type SwapType string

const (
	None    SwapType = "none"
	Linear  SwapType = "linear"  // u本位
	Inverse SwapType = "inverse" //  币本位
)

type Market struct {
	Symbol     string
	Exchange   Exchange
	MarketType MarketType
	SwapType   SwapType
}
