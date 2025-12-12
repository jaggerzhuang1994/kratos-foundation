package config

import "github.com/google/wire"

func WithoutFileConfigSource() FileConfigSource {
	return nil
}

func WithoutConsulConfigSource() ConsulConfigSource {
	return nil
}

var ProviderSet = wire.NewSet(
	NewConfig,
)
