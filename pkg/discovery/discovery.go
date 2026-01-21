package discovery

import (
	"github.com/go-kratos/kratos/contrib/registry/consul/v2"
	"github.com/go-kratos/kratos/v2/registry"
	consul2 "github.com/jaggerzhuang1994/kratos-foundation/pkg/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
)

type Discovery = registry.Discovery

func NewDiscovery(
	log log.Log,
	config Config,
	client consul2.Client,
) Discovery {
	log = log.WithModule("discovery")

	if client == nil {
		log.Warn("discovery not loaded: consul client not initialized")
		return nil
	}

	var opts = []consul.Option{
		consul.WithTimeout(config.GetTimeout().AsDuration()),
		consul.WithDatacenter(consul.Datacenter(config.GetDc().String())),
	}

	return consul.New(client, opts...)
}
