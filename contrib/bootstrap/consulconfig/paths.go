package consulconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
)

// NewDefaultRemoteConfigPathsProvider 返回按优先级排列的十二层远程路径。
// 环境单文件保留在环境目录，避免被基础层 name/*.yaml 匹配而混入其他环境。
// configs 先于 secrets；每组公共先于应用、基础先于环境、单文件先于同名目录片段。
func NewDefaultRemoteConfigPathsProvider() bootstrap.RemoteConfigPathsProvider {
	return func(_ appinfo.AppInfo, environment string, directory bootstrap.RemoteConfigDirName, name bootstrap.RemoteConfigName) []string {
		dir := string(directory)
		configName := string(name)
		return []string{
			"configs/common*.yaml",
			"configs/" + environment + "/common*.yaml",
			"configs/" + dir + "/" + configName + ".yaml",
			"configs/" + dir + "/" + configName + "/*.yaml",
			"configs/" + dir + "/" + environment + "/" + configName + ".yaml",
			"configs/" + dir + "/" + configName + "/" + environment + "/*.yaml",
			"secrets/common*.yaml",
			"secrets/" + environment + "/common*.yaml",
			"secrets/" + dir + "/" + configName + ".yaml",
			"secrets/" + dir + "/" + configName + "/*.yaml",
			"secrets/" + dir + "/" + environment + "/" + configName + ".yaml",
			"secrets/" + dir + "/" + configName + "/" + environment + "/*.yaml",
		}
	}
}

// NewDefaultLocalConfigPathsProvider 返回文件优先、目录按应用展开、否则按 glob 加载的路径函数。
// Stat 仅在调用返回的函数时执行；已有字面路径中的通配符字符不作 glob 解释。
func NewDefaultLocalConfigPathsProvider() bootstrap.LocalConfigPathsProvider {
	return func(info appinfo.AppInfo, environment string, location bootstrap.LocalConfigPath) ([]string, error) {
		path := string(location)
		fileInfo, err := os.Stat(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("stat local config path: %w", err)
		}
		if err == nil && fileInfo.IsDir() {
			return []string{filepath.Join(path, info.Name()+".yaml"), filepath.Join(path, environment, info.Name()+".yaml")}, nil
		}
		// 普通文件原样返回；不存在的路径留给文件源展开 glob 并处理无匹配情况。
		return []string{path}, nil
	}
}
