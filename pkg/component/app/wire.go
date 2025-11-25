package app

import (
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(
	NewConsulRegistry,
	NewConfig,
	NewHook,
	NewApp,
)
