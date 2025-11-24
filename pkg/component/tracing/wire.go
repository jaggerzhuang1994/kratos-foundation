package tracing

import "github.com/google/wire"

var ProviderSet = wire.NewSet(
	NewConfig,
	NewTracing,
)

var _ = ProviderSet
