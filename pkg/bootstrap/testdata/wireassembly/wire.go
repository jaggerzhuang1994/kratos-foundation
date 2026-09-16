//go:build wireinject

package wireassembly

import (
	"github.com/go-kratos/kratos/v2"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/bootstrap/consulconfig"
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

func initialize(sources config.Sources, version string, decorate app.ContextDecorator) (*assembly, func(), error) {
	wire.Build(
		newRegistrar,
		bootstrap.NewConfigManager,
		newSpec,
		app.NewSpec, server.NewSpec, job.NewSpec,
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
		tracing.NewProvider,
		tracing.NewTracing,
		bootstrap.NewTracingBootstrap,
		database.NewManager,
		newBusinessServer,
		newBootstrap,
		bootstrap.NewInfrastructureBootstrap,

		bootstrap.NewServerBootstrap, bootstrap.NewJobBootstrap, bootstrap.NewRuntimeBootstrap,
		bootstrap.NewApplicationBootstrap,
		bootstrap.NewKratosApp,
		wire.Struct(new(assembly), "*"),
	)
	return nil, nil, nil
}

func initializeComponents(sources config.Sources, version string) (*kratos.App, func(), error) {
	wire.Build(
		newRegistrar, bootstrap.NewConfigManager, app.NewSpec, server.NewSpec, job.NewSpec, app.NewConfig, app.NewStopPolicy,
		appinfo.New, log.NewLogger,
		wire.Bind(new(kratoslog.Logger), new(log.Logger)),
		metrics.NewProvider, metrics.NewMetrics, tracing.NewProvider,
		bootstrap.NewAppInfoBootstrap, bootstrap.NewLogBootstrap,
		bootstrap.NewMetricsBootstrap, bootstrap.NewTracingBootstrap,
		bootstrap.NewInfrastructureBootstrap, newSpec,
		componentsBoot, bootstrap.NewServerBootstrap, bootstrap.NewJobBootstrap, bootstrap.NewRuntimeBootstrap,
		bootstrap.NewApplicationBootstrap, bootstrap.NewKratosApp,
	)
	return nil, nil, nil
}

func newRegistrar() registry.Registrar { return nil }

func initializeDrivers(info appinfo.AppInfo, localConfigPath bootstrap.LocalConfigPath, directory bootstrap.RemoteConfigDirName) (*driverAssembly, func(), error) {
	wire.Build(bootstrap.BaseProviderSet, consulconfig.ProviderSet, componentsBoot, wire.Struct(new(driverAssembly), "*"))
	return nil, nil, nil
}

func initializeDriversWithDirectoryProvider(info appinfo.AppInfo, localConfigPath bootstrap.LocalConfigPath) (*driverAssembly, func(), error) {
	wire.Build(bootstrap.BaseProviderSet, consulconfig.ProviderSet, customRemoteConfigDirName, componentsBoot, wire.Struct(new(driverAssembly), "*"))
	return nil, nil, nil
}

func newSpec(application *app.Spec, servers *server.Spec, jobs *job.Spec, sources config.Sources) *bootstrap.Spec {
	return bootstrap.NewSpec(application, servers, jobs, bootstrap.ConfigSources{}).Configuration(func() (config.Sources, error) { return sources, nil })
}
