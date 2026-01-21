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
	log = log.WithModule("registry")

	if client == nil {
		log.Warn("registry not loaded: consul client not initialized")
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
