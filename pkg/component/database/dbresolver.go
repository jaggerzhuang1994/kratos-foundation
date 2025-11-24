package database

import (
	"context"
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

type DbResolver gorm.Plugin

func NewDbResolver(cfg *Config) gorm.Plugin {
	resolver := &dbresolver.DBResolver{}
	for name, connConf := range cfg.GetConnections() {
		resolverConf := dbresolver.Config{
			TraceResolverMode: connConf.GetTraceResolverMode(),
		}
		resolverConf.Sources = []gorm.Dialector{newConnection(connConf)}
		resolverConf.Replicas = utils.Map(connConf.GetReplicas(), func(conf *kratos_foundation_pb.DatabaseComponentConfig_Database_Connection_Dialector) gorm.Dialector {
			return newConnection(conf)
		})
		datas := append(connConf.GetDatas(), connName(name)) // 关联这个数据库链接，之后可以通过 dbresolver.Use(connName(connName)) 来指定链接
		resolver.Register(resolverConf, utils.MapToAny(datas)...)
	}

	return resolver
}

func connName(name string) string {
	return fmt.Sprintf("conn:%s", name)
}

type useConnectionKey struct{}

// UseConnection 使用指定数据库链接
func UseConnection(ctx context.Context, connection string) context.Context {
	return context.WithValue(ctx, useConnectionKey{}, connection)
}

type useOperationKey struct{}

// UseWrite 使用读链接
func UseWrite(ctx context.Context) context.Context {
	return context.WithValue(ctx, useOperationKey{}, dbresolver.Write)
}

// UseRead 使用写链接
func UseRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, useOperationKey{}, dbresolver.Read)
}
