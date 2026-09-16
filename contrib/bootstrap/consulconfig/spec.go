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
