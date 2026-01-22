package config

import (
	"github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
)

func NewConfig(
	fileSource FileSource,
	consulSource ConsulSource,
) (Config, func(), error) {
	var err error

	// 过滤掉 nil
	var sources = utils.FilterZero([]config.Source{fileSource, consulSource})

	// 排优先级
	// 如果是本地环境，则文件优先级更高
	if env.IsLocal() {
		sources = utils.Reverse(sources)
	}

	// 配置源
	c := config.New(config.WithSource(NewPriorityConfigSource(sources)))
	err = c.Load()
	if err != nil {
		_ = c.Close() // release config watcher
		return nil, nil, err
	}

	return c, func() {
		_ = c.Close()
	}, nil
}
