package bootstrap

import (
	"fmt"
	"strings"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// RemoteConfigDirName 是业务显式提供的远程配置目录名，不设默认值。
type RemoteConfigDirName string

// LocalConfigPath 是本地文件、目录或 glob 路径，区分 Wire 中的其他字符串依赖。
type LocalConfigPath string

// RemoteConfigPathsProvider 按目录、应用名与环境生成有序远程路径。
// 路径可包含配置源支持的 glob 模式；每次调用返回独立列表。
type RemoteConfigPathsProvider func(directory RemoteConfigDirName, name, environment string) []string

// LocalConfigPathsProvider 按本地路径、应用名与环境生成有序本地路径。
// 文件系统错误必须返回，不能通过空列表隐藏失败；路径解析延迟到配置加载阶段。
type LocalConfigPathsProvider func(location LocalConfigPath, name, environment string) ([]string, error)

// ConfigSources 描述应用默认配置来源，供 Wire 按具体类型注入。
// 零值不登记默认来源，适用于通过 Spec.Configuration 显式声明所有来源的应用。
// 启用默认来源时身份、路径函数及来源构造函数均须提供；路径由选中的来源校验。
// RemoteDir 仅在非 local 环境要求非空；NewSpec 复制描述，函数及 AppInfo 仍共享。
// 构造函数只创建延迟加载器，配置资源由 NewConfigManager 的 cleanup 释放。
type ConfigSources struct {
	AppInfo      appinfo.AppInfo
	LocalPath    LocalConfigPath
	RemoteDir    RemoteConfigDirName
	LocalPaths   LocalConfigPathsProvider
	RemotePaths  RemoteConfigPathsProvider
	LocalSource  func(...string) config.SourceLoader
	RemoteSource func(...string) config.SourceLoader
}

// loader 在 NewSpec 中固定环境，路径解析和来源 I/O 延迟到 NewConfigManager。
func (sources ConfigSources) loader() config.SourceLoader {
	if sources.LocalSource == nil && sources.RemoteSource == nil && sources.AppInfo == nil && sources.LocalPaths == nil && sources.RemotePaths == nil && sources.LocalPath == "" && sources.RemoteDir == "" {
		return nil
	}
	if sources.AppInfo == nil || sources.LocalPaths == nil || sources.RemotePaths == nil || sources.LocalSource == nil || sources.RemoteSource == nil {
		panic("bootstrap: incomplete config sources")
	}
	environment := env.AppEnv()
	return func() (config.Sources, error) {
		var loader config.SourceLoader
		if environment == env.Local {
			paths, err := sources.LocalPaths(sources.LocalPath, sources.AppInfo.Name(), environment)
			if err != nil {
				return nil, fmt.Errorf("resolve local config paths: %w", err)
			}
			loader = sources.LocalSource(paths...)
		} else {
			if strings.TrimSpace(string(sources.RemoteDir)) == "" {
				return nil, fmt.Errorf("remote config directory is required")
			}
			loader = sources.RemoteSource(sources.RemotePaths(sources.RemoteDir, sources.AppInfo.Name(), environment)...)
		}
		loaded, err := loader()
		if err != nil {
			return nil, err
		}
		if len(loaded) == 0 {
			// 无匹配来源或 Consul 禁用时沿用 env 启动，明确记录降级而不静默改用另一后端。
			log.WithModule("bootstrap").With("event", "config.sources.empty", "env", environment, "name", sources.AppInfo.Name()).Warn("No configuration sources available; continuing with env and any additional sources")
		}
		return loaded, nil
	}
}
