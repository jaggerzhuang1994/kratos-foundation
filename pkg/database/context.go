package database

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

type useConnectionKey struct{}
type useOperationKey struct{}
type useTxKey struct{}

// 使用事务
func useTx(ctx context.Context, db *gorm.DB) context.Context {
	return context.WithValue(ctx, useTxKey{}, db)
}

// 获取事务
func getTx(ctx context.Context) (*gorm.DB, bool) {
	db, ok := ctx.Value(useTxKey{}).(*gorm.DB)
	return db, ok
}

// UseConnection 通过使用 dbresolver 来切换数据库连接
func UseConnection(ctx context.Context, connection string) context.Context {
	return context.WithValue(ctx, useConnectionKey{}, connection)
}

// 获取是否指定连接
func getConnection(ctx context.Context) (string, bool) {
	connection, ok := ctx.Value(useConnectionKey{}).(string)
	return connection, ok
}

// UseWrite 使用读链接
func UseWrite(ctx context.Context) context.Context {
	return context.WithValue(ctx, useOperationKey{}, dbresolver.Write)
}

// UseRead 使用写链接
func UseRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, useOperationKey{}, dbresolver.Read)
}

// 是否指定读/写连接
func getOperation(ctx context.Context) (dbresolver.Operation, bool) {
	operation, ok := ctx.Value(useOperationKey{}).(dbresolver.Operation)
	return operation, ok
}
