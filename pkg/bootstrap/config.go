package bootstrap

import (
	"errors"
	"strings"

	fileconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/file"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// LocalConfigPath 是用户提供的本地文件、目录或 filepath.Glob 表达式。
type LocalConfigPath string

// LocalConfigSources 是本地配置源，交给 Manager 后不再修改。
type LocalConfigSources []config.Source

// RemoteConfigDirName 是远程应用配置的单层目录名。
type RemoteConfigDirName string

// RemoteConfigPaths 是按覆盖优先级升序排列的远程路径。
type RemoteConfigPaths []string

// RemoteConfigSources 是远程配置源，交给 Manager 后不再修改。
type RemoteConfigSources []config.Source

// DefaultAppRemoteConfigDirNameProvider 默认使用应用的可执行文件名作为远程目录名。
func DefaultAppRemoteConfigDirNameProvider(info appinfo.AppInfo) RemoteConfigDirName {
	return RemoteConfigDirName(info.Name())
}

// NewLocalConfigSources 在 local 环境加载指定文件、glob 匹配的普通文件或目录直属的 *.yaml 文件。
// 按文件名字典序加载，后面的覆盖前面的；非 local 不访问本地路径。
func NewLocalConfigSources(location LocalConfigPath) (LocalConfigSources, error) {
	if !env.IsLocal() {
		return nil, nil
	}
	sources, err := fileconfig.NewSources(fileconfig.PathList{string(location)})
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, errors.New("local config files are unavailable")
	}
	return LocalConfigSources(sources), nil
}

// NewRemoteConfigPaths 返回公共、环境、应用、应用环境的八层配置路径。
// 环境和目录名在组装期固定；目录名不允许路径分隔符或 glob 元字符。
func NewRemoteConfigPaths(directory RemoteConfigDirName) (RemoteConfigPaths, error) {
	name := string(directory)
	if name == "" || name == "." || name == ".." || strings.TrimSpace(name) != name || strings.ContainsAny(name, "/\\*?[]") {
		return nil, errors.New("remote config directory must be a non-empty literal directory name")
	}
	environment := env.AppEnv()
	return RemoteConfigPaths{
		"configs/common.yaml", "configs/" + environment + "/common.yaml",
		"secrets/common.yaml", "secrets/" + environment + "/common.yaml",
		"configs/" + name + "/*.yaml", "configs/" + name + "/" + environment + "/*.yaml",
		"secrets/" + name + "/*.yaml", "secrets/" + name + "/" + environment + "/*.yaml",
	}, nil
}

// NewConfigSources 仅选择当前环境的来源，不混合、不回退到另一类来源。
// 返回独立切片，但其中的 Source 仍为共享对象，只交给一个 Manager 管理。
func NewConfigSources(local LocalConfigSources, remote RemoteConfigSources) config.Sources {
	selected := config.Sources(remote)
	kind := "remote"
	if env.IsLocal() {
		selected, kind = config.Sources(local), "local"
	}
	log.WithModule("bootstrap").With("function", "NewConfigSources", "kind", kind, "count", len(selected)).Info("Selected configuration sources for the current environment")
	return append(config.Sources(nil), selected...)
}
