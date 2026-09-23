package consulconfig

import (
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
)

// ProviderSet 配合 BaseProviderSet 使用；业务提供 AppInfo、LocalConfigPaths 和 RemoteConfigPaths。
var ProviderSet = wire.NewSet(
	bootstrap.NewSpec,
	NewConfigSources,
)
