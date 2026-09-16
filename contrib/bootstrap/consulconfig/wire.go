package consulconfig

import (
	"github.com/google/wire"
)

// ProviderSet 提供默认配置 Spec、八层远程路径及基于 AppInfo 的配置名称。
// 业务提供 AppInfo 与 LocalConfigPath，配合 bootstrap.BaseProviderSet 使用。
var ProviderSet = wire.NewSet(
	ProviderSetWithCustomRemoteConfigName,
	NewDefaultRemoteConfigName,
)

// ProviderSetWithCustomRemoteConfigName 使用业务提供的 RemoteConfigName 和默认八层路径。
// 不得与 ProviderSet 同时注册；自定义路径时直接组装 NewSpec 与所需 provider。
var ProviderSetWithCustomRemoteConfigName = wire.NewSet(
	NewSpec,
	NewDefaultRemoteConfigPathsProvider,
)
