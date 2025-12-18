package database

import (
	"context"
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

type DbResolver gorm.Plugin

func NewDbResolver(cfg *Config, defaultConnection DefaultConnection) (resolver DbResolver, clean func(), err error) {
	var rcs []func()
	clean = func() {
		for _, rc := range rcs {
			rc()
		}
	}
	defer func() {
		if err != nil {
			clean()
		}
	}()

	resolver = &dbresolver.DBResolver{}
	for name, connConf := range cfg.GetConnections() {
		var rc func()
		var dialector gorm.Dialector
		if name == cfg.GetDefault() {
			dialector = defaultConnection
		} else {
			dialector, rc, err = newConnection(connConf)
			if err != nil {
				return
			}
			rcs = append(rcs, rc)
		}
		resolverConf := dbresolver.Config{
			TraceResolverMode: connConf.GetTraceResolverMode(),
		}
		resolverConf.Sources = []gorm.Dialector{dialector}

		for _, conf := range connConf.GetReplicas() {
			dialector, rc, err = newConnection(conf)
			if err != nil {
				return
			}
			rcs = append(rcs, rc)
			resolverConf.Replicas = append(resolverConf.Replicas, dialector)
		}

		datas := append(connConf.GetDatas(), connName(name)) // 关联这个数据库链接，之后可以通过 dbresolver.Use(connName(connName)) 来指定链接
		resolver.(*dbresolver.DBResolver).Register(resolverConf, utils.MapToAny(datas)...)
	}
	return
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
