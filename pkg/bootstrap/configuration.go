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

// RemoteConfigName 是远程配置名称，与应用身份及远程目录独立。
type RemoteConfigName string

// LocalConfigPath 是本地文件、目录或 glob 路径，区分 Wire 中的其他字符串依赖。
type LocalConfigPath string

// RemoteConfigPathsProvider 按目录、配置名称与环境生成有序远程路径。
// 路径可包含配置源支持的 glob 模式；每次调用返回独立列表。
type RemoteConfigPathsProvider func(info appinfo.AppInfo, environment string, directory RemoteConfigDirName, name RemoteConfigName) []string

// LocalConfigPathsProvider 按本地路径与环境生成有序本地路径。
// 文件系统错误必须返回，不能通过空列表隐藏失败；路径解析延迟到配置加载阶段。
type LocalConfigPathsProvider func(info appinfo.AppInfo, environment string, location LocalConfigPath) ([]string, error)

// ConfigSources 描述应用默认配置来源，供 Wire 按具体类型注入。
// 零值不登记默认来源，适用于通过 Spec.Configuration 显式声明所有来源的应用。
// 启用默认来源时身份、路径函数及来源构造函数均须提供；路径由选中的来源校验。
// NewSpec 复制描述，函数及 AppInfo 仍共享；构造阶段不执行配置 I/O。
type ConfigSources struct {
	// AppInfo 提供配置路径解析所需的应用身份。
	AppInfo appinfo.AppInfo
	// LocalPath 指定 local 环境使用的本地文件、目录或 glob。
	LocalPath LocalConfigPath
	// RemoteDir 指定远程配置目录；非 local 环境不能为空。
	RemoteDir RemoteConfigDirName
	// RemoteName 指定远程配置名称，独立于应用名称；非 local 环境不能为空。
	RemoteName RemoteConfigName
	// LocalPaths 在加载阶段解析有序本地路径并返回文件系统错误。
	LocalPaths LocalConfigPathsProvider
	// RemotePaths 按应用身份、环境及远程名称生成有序路径。
	RemotePaths RemoteConfigPathsProvider
	// LocalSource 按本地路径创建延迟加载器，资源由配置 Manager 释放。
	LocalSource func(...string) config.SourceLoader
	// RemoteSource 按远程路径创建延迟加载器，资源由配置 Manager 释放。
	RemoteSource func(...string) config.SourceLoader
}

// loader 在 NewSpec 中固定环境，路径解析和来源 I/O 延迟到 NewConfigManager。
func (sources ConfigSources) loader() config.SourceLoader {
	if sources.LocalSource == nil && sources.RemoteSource == nil && sources.AppInfo == nil && sources.LocalPaths == nil && sources.RemotePaths == nil && sources.LocalPath == "" && sources.RemoteDir == "" && sources.RemoteName == "" {
		return nil
	}
	if sources.AppInfo == nil || sources.LocalPaths == nil || sources.RemotePaths == nil || sources.LocalSource == nil || sources.RemoteSource == nil {
		panic("bootstrap: incomplete config sources")
	}
	environment := env.AppEnv()
	return func() (config.Sources, error) {
		var loader config.SourceLoader
		if environment == env.Local {
			paths, err := sources.LocalPaths(sources.AppInfo, environment, sources.LocalPath)
			if err != nil {
				return nil, fmt.Errorf("resolve local config paths: %w", err)
			}
			loader = sources.LocalSource(paths...)
		} else {
			if strings.TrimSpace(string(sources.RemoteDir)) == "" {
				return nil, fmt.Errorf("remote config directory is required")
			}
			// 名称由组装层提供，避免自定义配置名称改变应用身份。
			if strings.TrimSpace(string(sources.RemoteName)) == "" {
				return nil, fmt.Errorf("remote config name is required")
			}
			loader = sources.RemoteSource(sources.RemotePaths(sources.AppInfo, environment, sources.RemoteDir, sources.RemoteName)...)
		}
		loaded, err := loader()
		if err != nil {
			return nil, err
		}
		if len(loaded) == 0 {
			// 无匹配来源或 Consul 禁用时沿用 env 启动，明确记录降级而不静默改用另一后端。
			log.WithModule("bootstrap").With("function", "ConfigSources.loader", "event", "config.sources.empty", "remote_name", sources.RemoteName, "env", environment, "name", sources.AppInfo.Name()).Warn("No configuration sources available; continuing with env and any additional sources")
		}
		return loaded, nil
	}
}
