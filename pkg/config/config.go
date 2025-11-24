package config

import (
	"github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/env"
)

func NewConfig(localSource config.Source, remoteSource config.Source) (config.Config, func(), error) {
	var scs []config.Source

	// 如果是本地环境，本地source优先级更高
	if env.IsLocal() {
		if remoteSource != nil {
			scs = append(scs, remoteSource)
		}
		if localSource != nil {
			scs = append(scs, localSource)
		}
	} else {
		// 其他环境远程配置优先级更高
		if localSource != nil {
			scs = append(scs, localSource)
		}
		if remoteSource != nil {
			scs = append(scs, remoteSource)
		}
	}

	c := config.New(config.WithSource(NewPriorityConfigSource(scs)))
	if err := c.Load(); err != nil {
		_ = c.Close() // release config watcher
		return nil, nil, err
	}

	return c, func() {
		_ = c.Close()
	}, nil
}
