package database

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/dbroute"
	"gorm.io/gorm"
)

// TransactionManager 在 GORM 事务中执行回调，并通过回调 Context 传播事务。
type TransactionManager interface {
	Transaction(context.Context, func(context.Context) error, ...TransactionOption) error
}

type txOptions struct {
	// sqlOptions 本次事务的 SQL 选项；nil 使用驱动默认值，仅外层事务可显式设置，嵌套事务会拒绝。
	sqlOptions *sql.TxOptions
}

// TransactionOption 配置单次事务调用。
type TransactionOption func(o *txOptions)

// WithSQLTxOptions 指定底层 database/sql 事务选项；重复指定时以后一次为准，nil 表示使用驱动默认值。
func WithSQLTxOptions(opts *sql.TxOptions) TransactionOption {
	// 在创建 Option 时取值副本，避免调用方之后改动指针改变已组装的选项。
	var captured *sql.TxOptions
	if opts != nil {
		copied := *opts
		captured = &copied
	}
	return func(o *txOptions) { o.sqlOptions = captured }
}

// Transaction 在事务中执行 fc；使用回调 Context 的 Connection 会复用当前
// Manager 最内层的事务。事务 Context 仅在回调期间有效，不应逃逸到回调外。
func (mgr *manager) Transaction(ctx context.Context, fc func(context.Context) error, opts ...TransactionOption) error {
	opt := &txOptions{}
	for _, apply := range opts {
		apply(opt)
	}
	_, nested := getTx(ctx, mgr)
	if nested && opt.sqlOptions != nil {
		// GORM 用 savepoint 实现嵌套事务，database/sql 选项不会被应用。
		// 显式拒绝可避免调用方误以为隔离级别已生效。
		return errors.New("database SQL transaction options cannot be applied to a nested transaction")
	}
	db := mgr.Connection(ctx)
	root, name := mgr.connectionRoot(ctx)
	if db.Error != nil {
		// GORM 继续 Begin 会把 Manager 的连接选择错误与驱动错误拼接，
		// 但新错误链只保留后者；提前返回才能稳定使用 errors.Is。
		return db.Error
	}

	run := func(tx *gorm.DB) error {
		return fc(useTxOn(ctx, mgr, tx, root, name))
	}
	if opt.sqlOptions == nil {
		return db.Transaction(run)
	}
	return db.Transaction(run, opt.sqlOptions)
}

type useConnectionKey struct{}

// UsePrimary 让后续查询走主库而不是只读副本，用于写后立即读（read-your-writes）或不能容忍复制延迟的读。
// 没有配置 read_replicas 的连接不受影响；事务内的查询本来就在主库。
func UsePrimary(ctx context.Context) context.Context {
	return dbroute.WithPrimary(ctx)
}

type useTxKey struct{}

type transactionFrame struct {
	// owner 标识事务所属 Manager，避免跨实例误用。
	owner *manager
	// db 仅在当前事务回调期间有效的 GORM 事务。
	db *gorm.DB
	// root 与 name 是事务所在连接的 GORM 根实例与连接名，用于拒绝事务内切换到其他连接。
	root *gorm.DB
	name string
	// previous Context 中上一层事务，支持交错嵌套。
	previous *transactionFrame
}

// useTx 把当前 Manager 的事务压入 Context 链，以支持多 Manager 交错嵌套。
func useTx(ctx context.Context, owner *manager, db *gorm.DB) context.Context {
	return useTxOn(ctx, owner, db, nil, "")
}

// useTxOn 同 useTx，并记录事务所在连接。
func useTxOn(ctx context.Context, owner *manager, db, root *gorm.DB, name string) context.Context {
	previous, _ := ctx.Value(useTxKey{}).(*transactionFrame)
	return context.WithValue(ctx, useTxKey{}, &transactionFrame{
		owner:    owner,
		db:       db,
		root:     root,
		name:     name,
		previous: previous,
	})
}

// getTx 找到指定 Manager 在 Context 中最内层的事务。
func getTx(ctx context.Context, owner *manager) (*gorm.DB, bool) {
	frame, ok := getTxFrame(ctx, owner)
	if !ok {
		return nil, false
	}
	return frame.db, true
}

// getTxFrame 找到指定 Manager 在 Context 中最内层的事务 frame。
func getTxFrame(ctx context.Context, owner *manager) (*transactionFrame, bool) {
	// 显式保存前一层 frame，才能在不同 Manager 的事务交错嵌套时，
	// 找回当前 Manager 自己最内层的事务。
	frame, _ := ctx.Value(useTxKey{}).(*transactionFrame)
	for frame != nil {
		if frame.owner == owner {
			return frame, true
		}
		frame = frame.previous
	}
	return nil, false
}

// connectionRoot 返回事务将固定的连接：嵌套时沿用外层事务的连接，否则为 Context 选择或默认连接。
func (mgr *manager) connectionRoot(ctx context.Context) (*gorm.DB, string) {
	if frame, ok := getTxFrame(ctx, mgr); ok {
		return frame.root, frame.name
	}
	if name, explicit := getConnection(ctx); explicit {
		return mgr.connections[name], name
	}
	for name, db := range mgr.connections {
		if db == mgr.db {
			return db, name
		}
	}
	return mgr.db, ""
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
