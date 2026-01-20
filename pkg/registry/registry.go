package registry

import (
	"github.com/go-kratos/kratos/contrib/registry/consul/v2"
	"github.com/go-kratos/kratos/v2/registry"
	consul2 "github.com/jaggerzhuang1994/kratos-foundation/pkg/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
)

type Registrar = registry.Registrar

func NewRegistry(
	log log.Log,
	config Config,
	client consul2.Client,
) Registrar {
	if client == nil {
		log.Warn("not load registry: consul not initialized")
		return nil
	}

	var opts = []consul.Option{
		consul.WithHealthCheck(!config.GetDisableHealthCheck()),
		consul.WithHeartbeat(!config.GetDisableHeartbeat()),
		consul.WithHealthCheckInterval(int(config.GetHealthcheckInternal().GetSeconds())),
		consul.WithDeregisterCriticalServiceAfter(int(config.GetDeregisterCriticalServiceAfter().GetSeconds())),
		consul.WithTags(config.GetTags()),
	}

	return consul.New(client, opts...)
}
