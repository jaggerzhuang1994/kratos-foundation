package consulconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
)

// NewDefaultRemoteConfigPathsProvider 返回按优先级排列的十二层远程路径。
// configs 先于 secrets；每组公共先于应用、基础先于环境、单文件先于同名目录片段。
func NewDefaultRemoteConfigPathsProvider() bootstrap.RemoteConfigPathsProvider {
	return func(directory bootstrap.RemoteConfigDirName, name, environment string) []string {
		dir := string(directory)
		return []string{
			"configs/common*.yaml",
			"configs/" + environment + "/common*.yaml",
			"configs/" + dir + "/" + name + ".yaml",
			"configs/" + dir + "/" + name + "/*.yaml",
			"configs/" + dir + "/" + environment + "/" + name + ".yaml",
			"configs/" + dir + "/" + environment + "/" + name + "/*.yaml",
			"secrets/common*.yaml",
			"secrets/" + environment + "/common*.yaml",
			"secrets/" + dir + "/" + name + ".yaml",
			"secrets/" + dir + "/" + name + "/*.yaml",
			"secrets/" + dir + "/" + environment + "/" + name + ".yaml",
			"secrets/" + dir + "/" + environment + "/" + name + "/*.yaml",
		}
	}
}

// NewDefaultLocalConfigPathsProvider 返回文件优先、目录按应用展开、否则按 glob 加载的路径函数。
// Stat 仅在调用返回的函数时执行；已有字面路径中的通配符字符不作 glob 解释。
func NewDefaultLocalConfigPathsProvider() bootstrap.LocalConfigPathsProvider {
	return func(location bootstrap.LocalConfigPath, name, environment string) ([]string, error) {
		path := string(location)
		info, err := os.Stat(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("stat local config path: %w", err)
		}
		if err == nil && info.IsDir() {
			return []string{filepath.Join(path, name+".yaml"), filepath.Join(path, environment, name+".yaml")}, nil
		}
		// 普通文件原样返回；不存在的路径留给文件源展开 glob 并处理无匹配情况。
		return []string{path}, nil
	}
}
