package consul

import "github.com/google/wire"

var ProviderSet = wire.NewSet(
	NewConsul,
	NewConsulRegistry,
	NewKratosRegistry,
	NewKratosDiscovery,
)
