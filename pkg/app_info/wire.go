package app_info

import "github.com/google/wire"

var ProviderSet = wire.NewSet(
	NewAppInfo,
	NewServiceAttributes,
)
