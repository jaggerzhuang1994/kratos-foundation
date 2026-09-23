package bootstrap

import (
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/client"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/oss"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

// BaseProviderSet 提供共享领域声明、基础资源和应用组装阶段。
// 业务须提供 AppInfo、Boot，以及唯一的 *Spec provider；可选配置约定由 contrib 或业务提供该 Spec。
// 分组只便于阅读，实际构造顺序由 Wire 依赖关系决定。
var BaseProviderSet = wire.NewSet(
	// 共享声明由同一依赖图复用，业务不应再次提供这些类型。
	app.NewSpec,
	server.NewSpec,
	job.NewSpec,

	// 配置先由业务 Spec 声明来源，再按依赖关系构造 Manager 和应用配置。
	NewConfigManager,
	app.NewConfig,
	app.NewStopPolicy,

	// 观测依赖及其接口绑定供应用和队列复用。
	log.NewLogger,
	wire.Bind(new(kratoslog.Logger), new(log.Logger)),
	metrics.NewProvider, metrics.NewMetrics,
	tracing.NewProvider, tracing.NewTracing,
	queue.NewObservability,

	// 资源管理器按配置选择具体驱动；注册、发现和客户端共享 Factory。
	database.NewManager,
	redis.NewManager,
	kafka.NewClientFactory,
	oss.NewManager,
	registry.NewFactory,
	app.NewRegistrar,
	wire.Bind(new(client.DiscoveryResolver), new(*registry.Factory)),
	client.NewFactory,

	// 完成标记形成基础设施 → 服务器 → 任务 → 应用的构造依赖链。
	NewAppInfoBootstrap,
	NewLogBootstrap,
	NewTracingBootstrap,
	NewMetricsBootstrap,
	NewInfrastructureBootstrap,
	NewServerBootstrap,
	NewJobBootstrap,
	NewRuntimeBootstrap,
	NewApplicationBootstrap,
	NewKratosApp,
)
