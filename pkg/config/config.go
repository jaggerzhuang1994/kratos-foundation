// Package config 提供配置管理功能，支持多配置源合并与优先级控制
// 支持的配置源包括文件配置和 Consul 配置中心
package config

import (
	"github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
)

// NewConfig 创建配置实例，支持文件和 Consul 两种配置源
//
// 参数：
//   - fileSource: 文件配置源（如 config.yaml），可以为 nil
//   - consulSource: Consul 配置源，可以为 nil
//
// 返回：
//   - Config: 配置实例
//   - func(): 清理函数，用于关闭配置和释放资源
//   - error: 错误信息
//
// 配置优先级规则：
//   - 本地环境（env.IsLocal()）：文件配置 > Consul 配置
//   - 便于本地开发调试，文件配置可以覆盖远程配置
//   - 非本地环境：Consul 配置 > 文件配置
//   - 生产环境优先使用远程配置，文件配置作为后备
//
// 注意事项：
//   - 如果两个参数都为 nil，会返回空配置
//   - 配置加载失败时会自动清理资源
//   - 调用方负责在程序退出时调用清理函数
//
// 示例：
//
//	conf, cleanup, err := config.NewConfig(fileSource, consulSource)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer cleanup()
func NewConfig(
	fileSource FileSource,
	consulSource ConsulSource,
) (Config, func(), error) {
	var err error

	// 过滤掉 nil 源，只保留有效的配置源
	var sources = utils.FilterZero([]config.Source{fileSource, consulSource})

	// 根据环境配置优先级
	// 本地开发环境：文件配置优先，方便调试
	// 生产环境：Consul 配置优先，便于集中管理
	if env.IsLocal() {
		sources = utils.Reverse(sources)
	}

	// 创建配置实例并加载配置
	c := config.New(config.WithSource(NewPriorityConfigSource(sources)))
	err = c.Load()
	if err != nil {
		_ = c.Close() // 加载失败时释放配置 watcher 资源
		return nil, nil, err
	}

	// 返回配置实例和清理函数
	return c, func() {
		_ = c.Close()
	}, nil
}
