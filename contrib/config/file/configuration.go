package file

import "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"

// AddConfigSource 声明文件配置源，路径副本在 Configuration 阶段解析。
// 不进行 init 注册；空路径不添加来源，实际监听和清理由 Manager 负责。
func AddConfigSource(paths ...string) config.SourceLoader {
	paths = append([]string(nil), paths...)
	return func() (config.Sources, error) {
		sources, err := NewSources(paths)
		return config.Sources(sources), err
	}
}
