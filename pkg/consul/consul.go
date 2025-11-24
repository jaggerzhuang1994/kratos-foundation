package consul

import (
	"net"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/env"
	"github.com/pkg/errors"
	"golang.org/x/exp/rand"
)

var DefaultIPResolver = net.LookupIP

func NewConsul() (*api.Client, error) {
	// 如果没有指定 api.HTTPAddrEnvName 并且是 local 环境，则不返回 consul 实例
	if env.GetEnv(api.HTTPAddrEnvName) == "" && env.IsLocal() {
		log.Warn("本地环境，不连接consul")
		return nil, nil
	}

	// 默认配置从 env 读取
	config := api.DefaultConfig()

	// 获取host
	host, err := parseHost(config.Address)
	if err != nil {
		return nil, errors.WithMessage(err, "无法解析consul address host")
	}

	// 如果host不是ip，则解析ip
	if !isIP(host) {
		var ips []net.IP
		ips, err = DefaultIPResolver(host)
		if err != nil {
			return nil, errors.WithMessage(err, "无法解析consul地址")
		}
		if len(ips) == 0 {
			return nil, errors.New("无法解析consul地址: 无地址")
		}
		// 这里选取策略后面在改 是否要使用一个稳定性的标识作为选取策略还是要随机
		// 解析host成功，随机取一个作为consul节点
		ip := ips[rand.New(rand.NewSource(uint64(time.Now().UnixNano()))).Intn(len(ips))]
		config.Address, _ = replaceHostname(config.Address, ip.String())
	}

	log.Info("connect to consul: ", config.Address)

	client, err := api.NewClient(config)
	if err != nil {
		return nil, errors.WithMessagef(err, "初始化consul实例失败 config=%v", config)
	}
	_, err = client.Status().Leader()
	if err != nil {
		return nil, errors.WithMessagef(err, "连接consul失败 config=%v", config)
	}

	return client, nil
}
