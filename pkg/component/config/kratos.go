package config

import (
	"github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/config/internal/source"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
)

type FileConfigSource []string
type ConsulConfigSource []string

func NewKratosConfig(
	consul *consul.Client,
	fileSourceList FileConfigSource,
	consulSourceList ConsulConfigSource,
) (config.Config, func(), error) {
	var fileSource config.Source
	var consulSource config.Source
	var err error

	if len(fileSourceList) > 0 {
		fileSource, err = source.NewFilePatternSource(fileSourceList)
		if err != nil {
			return nil, nil, err
		}
	}

	if consul != nil && len(consulSourceList) > 0 {
		consulSource = source.NewConsulSource(consul, consulSourceList)
	}

	var sources = []config.Source{fileSource, consulSource}
	// 如果是本地环境，则文件优先级更高
	if env.IsLocal() {
		sources = utils.Reverse(sources)
	}
	sources = utils.FilterZero(sources) // 过滤掉 nil

	c := config.New(config.WithSource(source.NewPriorityConfigSource(sources)))
	err = c.Load()
	if err != nil {
		_ = c.Close() // release config watcher
		return nil, nil, err
	}

	return c, func() {
		_ = c.Close()
	}, nil
}
