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

// RemoteConfigName 是远程配置所属名称，可独立于应用名由业务 provider 提供。
type RemoteConfigName string

// NewDefaultRemoteConfigName 默认使用应用名作为远程配置名称。
func NewDefaultRemoteConfigName(info appinfo.AppInfo) RemoteConfigName {
	return RemoteConfigName(info.Name())
}

// NewDefaultRemoteConfigPathsProvider 返回默认八层路径函数，每次调用生成独立列表。
// 初次加载按返回顺序覆盖；热更新遵循配置源 merge 规则。
func NewDefaultRemoteConfigPathsProvider() bootstrap.RemoteConfigPathsProvider {
	return func(name, env string) []string {
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
}

// NewSpec 声明默认配置源，供 Wire 与 bootstrap.BaseProviderSet 配合使用。
// local 使用 localConfigPath；其他环境使用业务提供的远程路径函数。
// 构造期只登记声明；配置源加载错误在 NewConfigManager 执行 Configuration 时返回。
func NewSpec(localConfigPath bootstrap.LocalConfigPath, remoteConfigName RemoteConfigName, remoteConfigPaths bootstrap.RemoteConfigPathsProvider) (*bootstrap.Spec, error) {
	environment := env.AppEnv()
	var loader config.SourceLoader
	if environment == env.Local {
		loader = fileconfig.AddConfigSource(string(localConfigPath))
	} else {
		paths := remoteConfigPaths(string(remoteConfigName), environment)
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
				"env", environment, "name", remoteConfigName,
			).Warn("No configuration sources available from default spec; continuing with env and any additional sources")
		}
		return sources, nil
	}); err != nil {
		return nil, err
	}
	return spec, nil
}
