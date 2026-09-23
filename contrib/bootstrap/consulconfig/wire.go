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

// ProviderSet 在 BaseProviderSet 上提供默认远程目录及组合函数；业务提供 AppInfo、LocalConfigPaths 和相对 RemoteConfigPaths。
var ProviderSet = wire.NewSet(BaseProviderSet, NewDefaultRemoteConfigDirs, NewDefaultRemoteConfigPaths)
