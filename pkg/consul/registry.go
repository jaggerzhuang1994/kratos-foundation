package consul

import (
	"github.com/go-kratos/kratos/contrib/registry/consul/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/env"
)

func NewConsulRegistry(client *Client) *consul.Registry {
	if client == nil {
		log.Warn("无consul连接，不提供consul服务注册")
		return nil
	}
	return consul.New(client, getConsulOpts()...)
}

func getConsulOpts() []consul.Option {
	var opts []consul.Option

	// 是否开启健康检查，默认开启
	if healthCheck := env.GetEnvAsBool("CONSUL_OPTION_HEALTH_CHECK"); healthCheck {
		opts = append(opts, consul.WithHealthCheck(healthCheck))
	}

	// consul 接口超时时间 默认10s
	if timeout := env.GetEnvAsDuration("CONSUL_OPTION_TIMEOUT"); timeout > 0 {
		opts = append(opts, consul.WithTimeout(timeout))
	}

	// consul 数据中心
	if dc := env.GetEnv("CONSUL_OPTION_DATACENTER"); dc != "" {
		opts = append(opts, consul.WithDatacenter(consul.Datacenter(dc)))
	}

	// 是否开启心跳检测 默认开启
	if heartbeat := env.GetEnvAsBool("CONSUL_OPTION_HEARTBEAT"); heartbeat {
		opts = append(opts, consul.WithHeartbeat(heartbeat))
	}

	// 健康检查间隔, 默认10s
	if healthcheckInterval := env.GetEnvAsInt("CONSUL_OPTION_HEALTHCHECK_INTERVAL"); healthcheckInterval > 0 {
		opts = append(opts, consul.WithHealthCheckInterval(healthcheckInterval))
	}

	// 多久后取消注册错误的服务（秒），默认10分钟
	if deregisterCriticalServiceAfter := env.GetEnvAsInt("CONSUL_OPTION_DEREGISTER_CRITICAL_SERVICE_AFTER"); deregisterCriticalServiceAfter > 0 {
		opts = append(opts, consul.WithDeregisterCriticalServiceAfter(deregisterCriticalServiceAfter))
	}

	return opts
}
