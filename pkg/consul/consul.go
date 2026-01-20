package consul

import (
	"net"

	"github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
	"github.com/pkg/errors"
)

type Client = *api.Client

var DefaultIPResolver = net.LookupIP

const DisableConsul = "DISABLE_CONSUL"

func NewConsul(log log.Log) (Client, error) {
	// 如果指定 DISABLE_CONSUL
	// 如果没有指定 api.HTTPAddrEnvName 并且是 local 环境，则不返回 consul 实例
	if env.GetEnvAsBool(DisableConsul) || (env.GetEnv(api.HTTPAddrEnvName) == "" && env.IsLocal()) {
		log.Info("consul is not initialized")
		return nil, nil
	}

	// 默认配置从 env 读取
	config := api.DefaultConfig()

	// 如果 address 是域名，则解析为具体 ip
	// 防止应用启动后，连接 consul 实例不稳定（会导致服务发现和注册等组件出错）
	err := resolveAddress(&config.Address, DefaultIPResolver)
	if err != nil {
		return nil, err
	}
	log.Info("consul.address: ", config.Address)

	client, err := api.NewClient(config)
	if err != nil {
		return nil, errors.WithMessage(err, "初始化 consul 失败")
	}
	_, err = client.Status().Leader()
	if err != nil {
		return nil, errors.WithMessage(err, "调用 consul.Leader 失败")
	}

	return client, nil
}
