package consulconfig

import (
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
)

// ProviderSet 使用默认本地与远程路径规则，为 bootstrap.NewSpec 提供具体的 ConfigSources 描述。
// 业务提供 AppInfo、bootstrap.LocalConfigPath、bootstrap.RemoteConfigDirName，配合 BaseProviderSet 使用。
// 自定义路径规则时显式组装 NewConfigSources、bootstrap.NewSpec 和业务路径 provider。
var ProviderSet = wire.NewSet(bootstrap.NewSpec, NewConfigSources, NewDefaultLocalConfigPathsProvider, NewDefaultRemoteConfigPathsProvider)
