package bootstrap

import (
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	consuldiscovery "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/discovery/consul"
	consulregistry "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/registry/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/client"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/oss"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

// BaseProviderSet 提供通用基础组装，内置默认的 nil Job Coordinator。
// 业务提供 AppInfo、服务注册/发现、config.Sources 和 Bootstrap。
var BaseProviderSet = wire.NewSet(BaseProviderSetWithCustomJobCoordinator, job.DefaultCoordinator)

// BaseProviderSetWithCustomJobCoordinator 由业务提供 job.ConcurrencyCoordinator。
// 与 BaseProviderSet 二选一；资源由 Wire 按依赖关系逆序释放。
var BaseProviderSetWithCustomJobCoordinator = wire.NewSet(
	config.NewManager,
	log.NewLogger, wire.Bind(new(kratoslog.Logger), new(log.Logger)),
	metrics.NewProvider, metrics.NewMetrics, tracing.NewProvider, tracing.NewTracing,
	database.NewManager, redis.NewManager, client.NewFactory, kafka.NewClientFactory, oss.NewManager,
	NewSpec, ApplicationSpec, app.NewConfig, NewStopPolicy,
	NewAppInfoBootstrap, NewLogBootstrap, NewTracingBootstrap, NewMetricsBootstrap,
	NewConfigObservabilityBootstrap, NewInfrastructureBootstrap,
	NewComponentsBootstrap, NewApplicationBootstrap, NewKratosApp,
)

// ConsulProviderSet 提供共享客户端、注册发现和按环境选择的 config.Sources。
// 业务提供 LocalConfigPath；远程目录默认使用 AppInfo.Name()。
var ConsulProviderSet = wire.NewSet(ConsulProviderSetWithCustomRemoteConfigDirName, DefaultAppRemoteConfigDirNameProvider)

// ConsulProviderSetWithCustomRemoteConfigDirName 由业务提供 RemoteConfigDirName。
// 与 ConsulProviderSet 二选一，可独立搭配任一 BaseProviderSet。
var ConsulProviderSetWithCustomRemoteConfigDirName = wire.NewSet(
	consul.NewOptions, consul.New,
	consulregistry.NewRegistry, consuldiscovery.NewDiscovery,
	NewLocalConfigSources, NewRemoteConfigPaths, NewRemoteConfigSources, NewConfigSources,
)
