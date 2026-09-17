package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// TransactionManager 在 GORM 事务中执行回调，并通过回调 Context 传播事务。
type TransactionManager interface {
	Transaction(context.Context, func(context.Context) error, ...TransactionOption) error
}

type txOptions struct {
	// sqlOptions 本次事务的 SQL 选项副本；仅外层事务可显式设置，嵌套事务会拒绝。
	sqlOptions *sql.TxOptions
	// sqlOptionsSet 区分未传选项与显式传入 nil，后者会被拒绝。
	sqlOptionsSet bool
}

// TransactionOption 配置单次事务调用。
type TransactionOption func(o *txOptions)

// WithSQLTxOptions 指定底层 database/sql 事务选项；重复指定时以后一次为准。
func WithSQLTxOptions(opts *sql.TxOptions) TransactionOption {
	if opts == nil {
		return func(o *txOptions) {
			o.sqlOptionsSet = true
			o.sqlOptions = nil
		}
	}
	// 在创建 Option 时就取值副本，避免调用方之后改动指针改变已组装的选项。
	captured := *opts
	return func(o *txOptions) {
		o.sqlOptionsSet = true
		copied := captured
		o.sqlOptions = &copied
	}
}

// Transaction 在事务中执行 fc；使用回调 Context 的 Connection 会复用当前
// Manager 最内层的事务。事务 Context 仅在回调期间有效，不应逃逸到回调外。
func (mgr *manager) Transaction(ctx context.Context, fc func(context.Context) error, opts ...TransactionOption) error {
	if fc == nil {
		return errors.New("database transaction callback is nil")
	}
	opt := &txOptions{}
	for index, fn := range opts {
		if fn == nil {
			return fmt.Errorf("database transaction option %d is nil", index)
		}
		fn(opt)
	}
	if opt.sqlOptionsSet && opt.sqlOptions == nil {
		return errors.New("database SQL transaction options are nil")
	}

	_, nested := getTx(ctx, mgr)
	if nested && opt.sqlOptionsSet {
		// GORM 用 savepoint 实现嵌套事务，database/sql 选项不会被应用。
		// 显式拒绝可避免调用方误以为隔离级别已生效。
		return errors.New("database SQL transaction options cannot be applied to a nested transaction")
	}
	db := mgr.Connection(ctx)
	if db.Error != nil {
		// GORM 继续 Begin 会把 Manager 的连接选择错误与驱动错误拼接，
		// 但新错误链只保留后者；提前返回才能稳定使用 errors.Is。
		return db.Error
	}

	run := func(tx *gorm.DB) error {
		return fc(useTx(ctx, mgr, tx))
	}
	if opt.sqlOptions == nil {
		return db.Transaction(run)
	}
	return db.Transaction(run, opt.sqlOptions)
}

type useConnectionKey struct{}
type useTxKey struct{}

type transactionFrame struct {
	// owner 标识事务所属 Manager，避免跨实例误用。
	owner *manager
	// db 仅在当前事务回调期间有效的 GORM 事务。
	db *gorm.DB
	// previous Context 中上一层事务，支持交错嵌套。
	previous *transactionFrame
}

// useTx 把当前 Manager 的事务压入 Context 链，以支持多 Manager 交错嵌套。
func useTx(ctx context.Context, owner *manager, db *gorm.DB) context.Context {
	previous, _ := ctx.Value(useTxKey{}).(*transactionFrame)
	return context.WithValue(ctx, useTxKey{}, &transactionFrame{
		owner:    owner,
		db:       db,
		previous: previous,
	})
}

// getTx 找到指定 Manager 在 Context 中最内层的事务。
func getTx(ctx context.Context, owner *manager) (*gorm.DB, bool) {
	// 显式保存前一层 frame，才能在不同 Manager 的事务交错嵌套时，
	// 找回当前 Manager 自己最内层的事务。
	frame, _ := ctx.Value(useTxKey{}).(*transactionFrame)
	for frame != nil {
		if frame.owner == owner {
			return frame.db, true
		}
		frame = frame.previous
	}
	return nil, false
}

// UseConnection 为后续数据库调用选择具名连接；已有事务仍固定原连接。
func UseConnection(ctx context.Context, connection string) context.Context {
	return context.WithValue(ctx, useConnectionKey{}, connection)
}

// getConnection 读取 Context 中的具名连接选择。
func getConnection(ctx context.Context) (string, bool) {
	connection, ok := ctx.Value(useConnectionKey{}).(string)
	return connection, ok
}
