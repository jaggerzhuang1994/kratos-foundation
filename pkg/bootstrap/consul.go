package bootstrap

import (
	"errors"

	consulconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
)

// NewRemoteConfigSources 只在非 local 环境构造 Consul 配置源。
// 源借用共享客户端，监听由 Manager 管理；远程模式禁止静默降级为空配置。
func NewRemoteConfigSources(client consul.Client, paths RemoteConfigPaths) (RemoteConfigSources, error) {
	if env.IsLocal() {
		return nil, nil
	}
	if client == nil {
		return nil, errors.New("remote config requires an enabled consul client")
	}
	if len(paths) == 0 {
		return nil, errors.New("remote config paths are empty")
	}
	sources, err := consulconfig.NewSources(client, consulconfig.PathList(paths))
	return RemoteConfigSources(sources), err
}
