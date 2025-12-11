package config

import (
	"github.com/go-kratos/kratos/contrib/config/consul/v2"
	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/log"
	consul2 "github.com/jaggerzhuang1994/kratos-foundation/pkg/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
)

func NewConsulSource(
	client *consul2.Client, // consul客户端
	configPathList []string, // 配置列表
) config.Source {
	if client == nil {
		log.Warn("consul config source is nil")
		return nil
	}
	log.Info("consul config list ", configPathList)
	// 所有配置路径构成一个优先级组，优先级按照路径顺序指定
	return NewPriorityConfigSource(utils.Map(configPathList, func(configPath string) config.Source {
		sc, _ := consul.New(client, consul.WithPath(configPath))
		return sc
	}))
}
