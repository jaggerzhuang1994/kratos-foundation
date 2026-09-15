package consul

import (
	baseconsul "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
)

// AddConfigSource 声明 Consul 配置源；Configuration 执行时才获取 env 驱动的单例。
// 空路径或 Consul 禁用时不添加来源。Manager 只关闭 watcher，不释放共享客户端。
func AddConfigSource(paths ...string) config.SourceLoader {
	paths = append([]string(nil), paths...)
	return func() (config.Sources, error) {
		if len(paths) == 0 {
			return nil, nil
		}
		client, disabled, err := baseconsul.Get()
		if err != nil {
			return nil, err
		}
		if disabled {
			return nil, nil
		}
		sources, err := newSources(client, paths)
		return config.Sources(sources), err
	}
}
