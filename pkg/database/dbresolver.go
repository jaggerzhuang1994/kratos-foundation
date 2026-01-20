package database

import (
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

type DbResolver interface {
	gorm.Plugin
	ConnName(string) string
}

type dbResolver struct {
	*dbresolver.DBResolver
}

func NewDbResolver(
	config Config,
	defaultConnection DefaultConnection,
	connectionFactory ConnectionFactory,
) (DbResolver, error) {
	var err error
	resolver := &dbResolver{&dbresolver.DBResolver{}}
	for name, connConf := range config.GetConnections() {
		var dialector gorm.Dialector
		if name == config.GetDefault() {
			dialector = defaultConnection
		} else {
			dialector, err = connectionFactory.Make(connConf)
			if err != nil {
				return nil, err
			}
		}
		resolverConf := dbresolver.Config{
			TraceResolverMode: connConf.GetTraceResolverMode(),
		}
		resolverConf.Sources = []gorm.Dialector{dialector}

		for _, conf := range connConf.GetReplicas() {
			dialector, err = connectionFactory.Make(conf)
			if err != nil {
				return nil, err
			}
			resolverConf.Replicas = append(resolverConf.Replicas, dialector)
		}

		// 关联这个数据库链接，之后可以通过 dbresolver.Use(connName) 来指定链接
		datas := append(connConf.GetDatas(), resolver.ConnName(name))
		resolver.Register(resolverConf, utils.MapToAny(datas)...)
	}
	return resolver, nil
}

func (r *dbResolver) ConnName(name string) string {
	return fmt.Sprintf("conn:%s", name)
}
