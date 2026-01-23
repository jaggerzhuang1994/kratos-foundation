package config

import (
	config "github.com/go-kratos/kratos/contrib/config/consul/v2"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
)

// ConsulSource 是 Consul 配置源的别名
// 用于从 Consul 配置中心读取配置
type ConsulSource = Source

// ConsulSourcePathList 定义 Consul 配置路径列表
// 每个路径对应 Consul KV 存储中的一个配置路径
// 列表中的路径按顺序构成优先级，后面的配置会覆盖前面的配置
type ConsulSourcePathList []string

// NewConsulSource 创建基于 Consul 的配置源
//
// 参数：
//   - client: Consul 客户端实例，用于连接 Consul 服务器
//   - log: 日志记录器，用于记录配置加载过程
//   - consulSourcePathList: Consul 配置路径列表，支持多路径配置合并
//
// 返回：
//   - ConsulSource: 配置源实例，如果参数校验失败返回 nil
//
// 配置合并规则：
//   - 多个配置路径会按顺序合并，后加载的配置会覆盖前面的配置
//   - 例如：["config/base", "config/prod"]，prod 中的配置会覆盖 base 中的同名配置
//
// 失败场景：
//   - consulSourcePathList 为空：记录警告日志，返回 nil
//   - client 为 nil：记录警告日志，返回 nil
//
// 示例：
//
//	// 创建 Consul 配置源
//	consulSource := config.NewConsulSource(
//	    consulClient,
//	    logger,
//	    config.ConsulSourcePathList{"/config/app/base", "/config/app/prod"},
//	)
//
//	// 使用配置源
//	conf, cleanup, err := config.NewConfig(nil, consulSource)
func NewConsulSource(
	client consul.Client, // consul 客户端
	log log.Log, // logger
	consulSourcePathList ConsulSourcePathList, // 配置列表
) ConsulSource {
	// 参数校验：配置路径列表不能为空
	if len(consulSourcePathList) == 0 {
		log.Warn("consul source not loaded: path is empty")
		return nil
	}

	// 参数校验：Consul 客户端必须已初始化
	if client == nil {
		log.Warn("consul source not loaded: consul client not initialized")
		return nil
	}

	log.Info("consul source path list:", consulSourcePathList)

	// 将多个配置路径转换为配置源列表
	// 每个路径都会创建一个独立的配置源
	// 所有配置源会被包装成优先级配置源，后面的配置会覆盖前面的配置
	return NewPriorityConfigSource(utils.Map(consulSourcePathList, func(configPath string) Source {
		sc, _ := config.New(client, config.WithPath(configPath))
		return sc
	}))
}
