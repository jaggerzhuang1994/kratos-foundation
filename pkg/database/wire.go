package database

import (
	"github.com/google/wire"
)

var _ TransactionManager = (Manager)(nil)

var ProviderSet = wire.NewSet(
	NewDefaultConfig,
	NewConfig,
	NewConnectionFactory,
	NewDefaultConnection,
	NewDbResolver,
	NewGormConfig,
	NewGormLogger,
	NewMetricsPlugin,
	NewTracingPlugin,
	NewManager,
	wire.Bind(new(TransactionManager), new(Manager)),
)
