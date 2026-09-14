//go:build wireinject

package wireassembly

import (
	"time"

	"github.com/go-kratos/kratos/v2"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

func initialize(sources config.Sources, version string, stopDelay time.Duration, decorate app.ContextDecorator) (*assembly, func(), error) {
	wire.Build(
		newRegistrar,
		config.NewManager,
		app.NewSpec,
		app.NewConfig,
		app.NewStopPolicy,
		appinfo.New,
		bootstrap.NewAppInfoBootstrap,
		log.NewLogger,
		bootstrap.NewLogBootstrap,
		wire.Bind(new(kratoslog.Logger), new(log.Logger)),
		metrics.NewProvider,
		metrics.NewMetrics,
		bootstrap.NewMetricsBootstrap,
		bootstrap.NewConfigObservabilityBootstrap,
		tracing.NewProvider,
		tracing.NewTracing,
		bootstrap.NewTracingBootstrap,
		database.NewManager,
		newServerSpec,
		server.NewRuntime,
		bootstrap.NewServerBootstrap,
		newBootstrap,
		bootstrap.NewInfrastructureBootstrap,
		bootstrap.NewBootstrap,
		bootstrap.NewKratosApp,
		wire.Struct(new(assembly), "*"),
	)
	return nil, nil, nil
}

func initializeComponents(sources config.Sources, version string) (*kratos.App, func(), error) {
	wire.Build(
		newRegistrar, job.DefaultCoordinator, config.NewManager, bootstrap.ApplicationSpec, app.NewConfig, bootstrap.NewStopPolicy,
		appinfo.New, log.NewLogger,
		wire.Bind(new(kratoslog.Logger), new(log.Logger)),
		metrics.NewProvider, metrics.NewMetrics, tracing.NewProvider,
		bootstrap.NewAppInfoBootstrap, bootstrap.NewLogBootstrap,
		bootstrap.NewMetricsBootstrap, bootstrap.NewTracingBootstrap,
		bootstrap.NewInfrastructureBootstrap, bootstrap.NewConfigObservabilityBootstrap, bootstrap.NewSpec,
		componentsBoot, bootstrap.NewComponentsBootstrap,
		bootstrap.NewApplicationBootstrap, bootstrap.NewKratosApp,
	)
	return nil, nil, nil
}

func newRegistrar() registry.Registrar { return nil }

// 消费完整公共集合，同时验证默认 Coordinator 可替换。
func initializeConsulBase(info appinfo.AppInfo, files bootstrap.LocalConfigPath) (*consulAssembly, func(), error) {
	wire.Build(bootstrap.BaseProviderSet, bootstrap.ConsulProviderSet, componentsBoot, wire.Struct(new(consulAssembly), "*"))
	return nil, nil, nil
}

func initializeDefaultResources(info appinfo.AppInfo, files bootstrap.LocalConfigPath) (*defaultResources, func(), error) {
	wire.Build(bootstrap.BaseProviderSet, bootstrap.ConsulProviderSet, wire.Struct(new(defaultResources), "*"))
	return nil, nil, nil
}

func initializeConsulCustomCoordinator(info appinfo.AppInfo, files bootstrap.LocalConfigPath, coordinator job.ConcurrencyCoordinator) (*consulAssembly, func(), error) {
	wire.Build(bootstrap.BaseProviderSetWithCustomJobCoordinator, bootstrap.ConsulProviderSet, componentsBoot, wire.Struct(new(consulAssembly), "*"))
	return nil, nil, nil
}

func initializeConsulCustomDirectory(info appinfo.AppInfo, files bootstrap.LocalConfigPath, directory bootstrap.RemoteConfigDirName) (*consulAssembly, func(), error) {
	wire.Build(bootstrap.BaseProviderSet, bootstrap.ConsulProviderSetWithCustomRemoteConfigDirName, componentsBoot, wire.Struct(new(consulAssembly), "*"))
	return nil, nil, nil
}

func initializeConsulCustomBoth(info appinfo.AppInfo, files bootstrap.LocalConfigPath, directory bootstrap.RemoteConfigDirName, coordinator job.ConcurrencyCoordinator) (*consulAssembly, func(), error) {
	wire.Build(bootstrap.BaseProviderSetWithCustomJobCoordinator, bootstrap.ConsulProviderSetWithCustomRemoteConfigDirName, componentsBoot, wire.Struct(new(consulAssembly), "*"))
	return nil, nil, nil
}

// 自定义注册、发现与配置可完全不引入 Consul 集合或路径类型。
func initializeCustomBackend(info appinfo.AppInfo, sources config.Sources, registrar registry.Registrar, discovery registry.Discovery) (*consulAssembly, func(), error) {
	wire.Build(bootstrap.BaseProviderSet, componentsBoot, wire.Struct(new(consulAssembly), "*"))
	return nil, nil, nil
}
