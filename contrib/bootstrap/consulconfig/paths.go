package consulconfig

import (
	"path"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
)

// RemoteConfigDirs 是业务指定的有序 Consul KV 目录列表。
type RemoteConfigDirs []string

// RemoteConfigPaths 是相对于每个远程目录的有序路径模板列表。
type RemoteConfigPaths []string

// NewDefaultRemoteConfigDirs 返回独立的默认远程目录列表，configs 的优先级低于 secrets。
func NewDefaultRemoteConfigDirs() RemoteConfigDirs {
	return RemoteConfigDirs{"configs", "secrets"}
}

// NewDefaultRemoteConfigPaths 按目录顺序展开每组相对路径，返回独立的完整路径列表。
// 不执行模板替换或路径模式解析；空目录列表或空路径列表会禁用远程配置来源。
func NewDefaultRemoteConfigPaths(dirs RemoteConfigDirs, paths RemoteConfigPaths) bootstrap.RemoteConfigPaths {
	result := make(bootstrap.RemoteConfigPaths, 0, len(dirs)*len(paths))
	for _, dir := range dirs {
		for _, pattern := range paths {
			result = append(result, path.Join(dir, pattern))
		}
	}
	return result
}
