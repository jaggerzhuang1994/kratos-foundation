// Package consulconfig 提供 local 文件、其他环境 Consul 的可选配置约定。
package consulconfig

import (
	consulsource "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/consul"
	fileconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/file"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
)

// NewConfigSources 组装业务提供的两组路径模板，不执行模板替换、环境选择或 I/O。
func NewConfigSources(info appinfo.AppInfo, localPaths bootstrap.LocalConfigPaths, remotePaths bootstrap.RemoteConfigPaths) bootstrap.ConfigSources {
	return bootstrap.ConfigSources{
		AppInfo:      info,
		LocalPaths:   localPaths,
		RemotePaths:  remotePaths,
		LocalSource:  fileconfig.AddConfigSource,
		RemoteSource: consulsource.AddConfigSource,
	}
}
