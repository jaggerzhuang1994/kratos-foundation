package bootstrap

import (
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	consulconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/consul"
	fileconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/file"
	consuldiscovery "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/discovery/consul"
	consulregistry "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/registry/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/client"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/oss"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

// ConsulBaseProviderSet 提供 Consul 模式下的完整基础组装与应用构造。
// 内置 job.DefaultCoordinator（nil），默认不启用跨进程协调。
// 业务提供 AppInfo、file.PathList、consul.PathList 和 Bootstrap。
var ConsulBaseProviderSet = wire.NewSet(ConsulBaseProviderSetWithCustomJobCoordinator, job.DefaultCoordinator)

// ConsulBaseProviderSetWithCustomJobCoordinator 提供相同基础组装，但由业务提供 job.ConcurrencyCoordinator。
// 与 ConsulBaseProviderSet 二选一；不可同时引入，否则产生重复 provider。
// Logger、可观测性、Spec、启动屏障与停机策略由此集合构造；默认资源构造按业务依赖选取。
// Consul 连接从环境读取，先于配置 Manager 构造；所有资源仍由 Wire 逆序释放。
var ConsulBaseProviderSetWithCustomJobCoordinator = wire.NewSet(
	consul.NewOptions, consul.New,
	fileconfig.NewSources, consulconfig.NewSources, NewConsulSources, config.NewManager,
	consulregistry.NewRegistry, consuldiscovery.NewDiscovery,
	log.NewLogger, wire.Bind(new(kratoslog.Logger), new(log.Logger)),
	metrics.NewProvider, metrics.NewMetrics, tracing.NewProvider, tracing.NewTracing,
	database.NewManager, redis.NewManager, client.NewFactory, kafka.NewClientFactory, oss.NewManager,
	NewSpec, ApplicationSpec, app.NewConfig, NewStopPolicy,
	NewAppInfoBootstrap, NewLogBootstrap, NewTracingBootstrap, NewMetricsBootstrap,
	NewConfigObservabilityBootstrap, NewInfrastructureBootstrap,
	NewComponentsBootstrap, NewApplicationBootstrap, NewKratosApp,
)

// NewConsulSources 组合有序文件源和 Consul 源，供配置 Manager 管理监听。
// local 环境本地优先，其他环境 Consul 优先；同组内后面的源覆盖前面的源。
func NewConsulSources(files fileconfig.Sources, remote consulconfig.Sources) config.Sources {
	sources := make(config.Sources, 0, len(files)+len(remote))
	if env.IsLocal() {
		return append(append(sources, remote...), files...)
	}
	return append(append(sources, files...), remote...)
}
