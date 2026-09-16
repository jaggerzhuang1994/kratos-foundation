package consulconfig

import (
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
)

// ProviderSet 使用 AppInfo.Name() 作为远程配置名称，配合 BaseProviderSet 使用。
// 业务提供 AppInfo、bootstrap.LocalConfigPath 和 bootstrap.RemoteConfigDirName。
var ProviderSet = wire.NewSet(ProviderSetWithCustomRemoteConfigName, NewDefaultRemoteConfigName)

// ProviderSetWithCustomRemoteConfigName 由业务额外提供 bootstrap.RemoteConfigName。
// 与 ProviderSet 二选一；自定义完整路径规则时显式组装各 provider。
var ProviderSetWithCustomRemoteConfigName = wire.NewSet(bootstrap.NewSpec, NewConfigSources, NewDefaultLocalConfigPathsProvider, NewDefaultRemoteConfigPathsProvider)
