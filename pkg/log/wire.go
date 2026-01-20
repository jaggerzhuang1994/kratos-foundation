package log

import (
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(
	NewDefaultConfig,
	NewPresetKv,
	NewLogger,
	NewLog,

	wire.Bind(new(UpdateLogger), new(Logger)),
	wire.Bind(new(log.Logger), new(Logger)),
)
