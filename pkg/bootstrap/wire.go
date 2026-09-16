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

// BaseProviderSet 按注册驱动组装配置、注册和发现；业务提供已声明 Configuration 的 *Spec、AppInfo 和 Boot。
// 包含 app/server/job 的 Spec 及 queue.Observability provider，同一依赖图内共享实例；所有注册与发现均通过具名驱动解析。
var BaseProviderSet = wire.NewSet(
	NewConfigManager, registry.NewFactory, app.NewRegistrar,
	wire.Bind(new(client.DiscoveryResolver), new(*registry.Factory)), client.NewFactory,
	log.NewLogger, wire.Bind(new(kratoslog.Logger), new(log.Logger)),
	metrics.NewProvider, metrics.NewMetrics, tracing.NewProvider, tracing.NewTracing,
	queue.NewObservability,
	database.NewManager, redis.NewManager, kafka.NewClientFactory, oss.NewManager,
	app.NewSpec, server.NewSpec, job.NewSpec, app.NewConfig, app.NewStopPolicy,
	NewAppInfoBootstrap, NewLogBootstrap, NewTracingBootstrap, NewMetricsBootstrap,
	NewInfrastructureBootstrap,
	NewServerBootstrap, NewJobBootstrap, NewRuntimeBootstrap, NewApplicationBootstrap, NewKratosApp,
)
