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

// RemoteConfigPaths 返回独立的八层 Consul 配置路径列表，调用方可修改。
// 保留八层初次加载顺序，后加载的路径覆盖前面的同名配置项。
// 热更新仍遵循官方 merge，不保证跨来源的固定覆盖优先级。
func RemoteConfigPaths(name, env string) []string {
	return []string{
		"configs/common*.yaml",
		"configs/" + env + "/common*.yaml",
		"secrets/common*.yaml",
		"secrets/" + env + "/common*.yaml",
		"configs/" + name + "/*.yaml",
		"configs/" + name + "/" + env + "/*.yaml",
		"secrets/" + name + "/*.yaml",
		"secrets/" + name + "/" + env + "/*.yaml",
	}
}
