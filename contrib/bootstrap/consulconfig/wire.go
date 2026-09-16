package consulconfig

import (
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
)

// ProviderSet 提供默认配置 Spec 和八层远程路径，配合 bootstrap.BaseProviderSet 使用。
// 业务提供 AppInfo 与 LocalConfigPath；自定义路径时改用 NewSpec 并注入自己的路径 provider。
var ProviderSet = wire.NewSet(
	NewSpec,
	wire.Value(bootstrap.RemoteConfigPathsProvider(RemoteConfigPaths)),
)
