package config

import "github.com/google/wire"

var ProviderSet = wire.NewSet(
	NewConsulSource,
	NewFileSource,
	NewConfig,
	NewKratosFoundationConfig,
)
