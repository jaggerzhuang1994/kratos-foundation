package database

import "github.com/google/wire"

var ProviderSet = wire.NewSet(
	NewConfig,
	NewGormLogger,
	NewGormConfig,
	NewDefaultConnection,
	NewTracingPlugin,
	NewMetricsPlugin,
	NewDbResolver,
	NewManager,
)
