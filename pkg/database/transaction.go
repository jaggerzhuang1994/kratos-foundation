package database

import (
	"context"
	"database/sql"

	"gorm.io/gorm"
)

// TransactionManager 事务管理器接口
type TransactionManager interface {
	Transaction(context.Context, func(context.Context) error, ...TransactionOption) error
}

// txOptions 事务选项
type txOptions struct {
	opts []*sql.TxOptions
}

// TransactionOption 事务参数选项
type TransactionOption func(o *txOptions)

// WithSqlTxOptions 指定事务参数
func WithSqlTxOptions(opts ...*sql.TxOptions) TransactionOption {
	return func(o *txOptions) {
		o.opts = append(o.opts, opts...)
	}
}

// Transaction 开启事务
func (mgr *manager) Transaction(ctx context.Context, fc func(ctx2 context.Context) (err error), opts ...TransactionOption) error {
	opt := &txOptions{}
	for _, fn := range opts {
		fn(opt)
	}
	return mgr.GetConnection(ctx).Transaction(func(tx *gorm.DB) error {
		return fc(useTx(ctx, tx))
	}, opt.opts...)
}
