package consul

import (
	"github.com/go-kratos/kratos/contrib/registry/consul/v2"
	"github.com/go-kratos/kratos/v2/registry"
)

func NewKratosRegistry(registry *consul.Registry) registry.Registrar {
	if registry == nil {
		return nil
	}
	return registry
}

func NewKratosDiscovery(registry *consul.Registry) registry.Discovery {
	if registry == nil {
		return nil
	}
	return registry
}
