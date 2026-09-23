package consulconfig

import (
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
)

// BaseProviderSet 配合 bootstrap.BaseProviderSet 使用；业务提供 AppInfo、LocalConfigPaths 和完整的 bootstrap.RemoteConfigPaths。
var BaseProviderSet = wire.NewSet(
	bootstrap.NewSpec,
	NewConfigSources,
)

// ProviderSet 在 BaseProviderSet 上提供路径组合函数；业务提供 AppInfo、LocalConfigPaths、ConsulConfigPrefix 和相对 ConsulConfigPaths。
var ProviderSet = wire.NewSet(BaseProviderSet, NewDefaultRemoteConfigPaths)
