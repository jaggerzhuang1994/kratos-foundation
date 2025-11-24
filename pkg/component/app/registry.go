package app

import (
	"github.com/go-kratos/kratos/contrib/registry/consul/v2"
	"github.com/go-kratos/kratos/v2/registry"
)

func NewConsulRegistry(r *consul.Registry) registry.Registrar {
	return r
}
