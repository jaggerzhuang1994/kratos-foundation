// Package consulconfig 提供 local 文件、其他环境 Consul 的可选应用组装约定。
package consulconfig

import (
	consulsource "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/consul"
	fileconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/file"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// NewSpec 声明默认配置源，供 Wire 与 bootstrap.BaseProviderSet 配合使用。
// local 使用 localConfigPath；其他环境使用业务提供的远程路径函数。
// 构造期只登记声明；配置源加载错误在 NewConfigManager 执行 Configuration 时返回。
func NewSpec(info appinfo.AppInfo, localConfigPath bootstrap.LocalConfigPath, remoteConfigPaths bootstrap.RemoteConfigPathsProvider) (*bootstrap.Spec, error) {
	environment := env.AppEnv()
	var loader config.SourceLoader
	if environment == env.Local {
		loader = fileconfig.AddConfigSource(string(localConfigPath))
	} else {
		paths := remoteConfigPaths(info.Name(), environment)
		loader = consulsource.AddConfigSource(paths...)
	}
	spec := bootstrap.NewSpec()
	if err := spec.Configuration(func() (config.Sources, error) {
		sources, err := loader()
		if err != nil {
			return nil, err
		}
		// 无额外来源时允许使用 env 启动，并在配置加载前提示降级。
		if len(sources) == 0 {
			log.WithModule("bootstrap/consulconfig").With(
				"environment", environment, "app", info.Name(),
			).Warn("No configuration sources available from default spec; continuing with env and any additional sources")
		}
		return sources, nil
	}); err != nil {
		return nil, err
	}
	return spec, nil
}

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
