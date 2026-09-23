package consulconfig

import (
	"path"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
)

// ConsulConfigPrefix 是业务指定的有序 Consul KV 路径前缀列表。
type ConsulConfigPrefix []string

// ConsulConfigPaths 是相对于每个远程前缀的有序路径模板列表。
type ConsulConfigPaths []string

// NewDefaultRemoteConfigPaths 按前缀顺序展开每组相对路径，返回独立的完整路径列表。
// 不执行模板替换或路径模式解析；空前缀列表或空路径列表会禁用远程配置来源。
func NewDefaultRemoteConfigPaths(prefixes ConsulConfigPrefix, paths ConsulConfigPaths) bootstrap.RemoteConfigPaths {
	result := make(bootstrap.RemoteConfigPaths, 0, len(prefixes)*len(paths))
	for _, prefix := range prefixes {
		for _, pattern := range paths {
			result = append(result, path.Join(prefix, pattern))
		}
	}
	return result
}
