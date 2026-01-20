package kratos_foundation

import (
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/client"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/context"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/database"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/discovery"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/server"
	_ "github.com/jaggerzhuang1994/kratos-foundation/pkg/setup"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/tracing"
)

// ProviderSet 按照以下顺序依赖
var ProviderSet = wire.NewSet(
	// 应用信息
	app_info.ProviderSet,
	// 日志
	log.ProviderSet,
	// consul
	consul.ProviderSet,
	// 配置
	config.ProviderSet,
	// 服务注册            // 服务发现              // 链路追踪           // 监控
	registry.ProviderSet, discovery.ProviderSet, tracing.ProviderSet, metrics.ProviderSet,
	// 数据库			  // redis
	database.ProviderSet, redis.ProviderSet,
	// job client server context
	job.ProviderSet,
	client.ProviderSet,
	server.ProviderSet,
	context.ProviderSet,
	// bootstrap
	app.ProviderSet,
)
