package config

import (
	config "github.com/go-kratos/kratos/contrib/config/consul/v2"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
)

type ConsulSource Source
type ConsulSourcePathList []string

func NewConsulSource(
	client consul.Client, // consul 客户端
	log log.Log, // logger
	consulSourcePathList ConsulSourcePathList, // 配置列表
) ConsulSource {
	if len(consulSourcePathList) == 0 {
		log.Warn("consul source not loaded: path is empty")
		return nil
	}
	if client == nil {
		log.Warn("consul source not loaded: consul client not initialized")
		return nil
	}
	log.Info("consul source path list:", consulSourcePathList)
	// 所有配置路径构成一个优先级组，优先级按照路径顺序指定
	return NewPriorityConfigSource(utils.Map(consulSourcePathList, func(configPath string) Source {
		sc, _ := config.New(client, config.WithPath(configPath))
		return sc
	}))
}
