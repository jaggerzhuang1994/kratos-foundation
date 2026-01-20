package app

import (
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(
	NewDefaultConfig,
	NewConfig,
	NewHook,
	NewApp,
)
