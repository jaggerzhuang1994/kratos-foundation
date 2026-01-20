package discovery

import (
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(
	NewDefaultConfig,
	NewConfig,
	NewDiscovery,
)
