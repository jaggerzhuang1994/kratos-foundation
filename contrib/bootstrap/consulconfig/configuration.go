// Package consulconfig 提供 local 文件、其他环境 Consul 的可选配置约定。
package consulconfig

import (
	consulsource "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/consul"
	fileconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/file"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
)

// NewConfigSources 为 bootstrap.NewSpec 组装默认来源描述，不执行环境选择或 I/O。
func NewConfigSources(info appinfo.AppInfo, localPath bootstrap.LocalConfigPath, directory bootstrap.RemoteConfigDirName, localPaths bootstrap.LocalConfigPathsProvider, remotePaths bootstrap.RemoteConfigPathsProvider) bootstrap.ConfigSources {
	return bootstrap.ConfigSources{
		AppInfo: info, LocalPath: localPath, RemoteDir: directory,
		LocalPaths: localPaths, RemotePaths: remotePaths,
		LocalSource: fileconfig.AddConfigSource, RemoteSource: consulsource.AddConfigSource,
	}
}
