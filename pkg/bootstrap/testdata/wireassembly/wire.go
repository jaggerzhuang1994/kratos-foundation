//go:build wireinject

package wireassembly

import (
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
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

func initialize(sources config.Sources, version string, decorate app.ContextDecorator) (*assembly, func(), error) {
	wire.Build(
		newRegistrar,
		config.NewManager,
		bootstrap.NewSpec,
		bootstrap.ApplicationSpec,
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
		job.DefaultCoordinator,
		bootstrap.NewServerBootstrap, bootstrap.NewJobBootstrap, bootstrap.NewRuntimeBootstrap,
		bootstrap.NewApplicationBootstrap,
		bootstrap.NewKratosApp,
		wire.Struct(new(assembly), "*"),
	)
	return nil, nil, nil
}

func initializeComponents(sources config.Sources, version string) (*kratos.App, func(), error) {
	wire.Build(
		newRegistrar, job.DefaultCoordinator, config.NewManager, bootstrap.ApplicationSpec, app.NewConfig, app.NewStopPolicy,
		appinfo.New, log.NewLogger,
		wire.Bind(new(kratoslog.Logger), new(log.Logger)),
		metrics.NewProvider, metrics.NewMetrics, tracing.NewProvider,
		bootstrap.NewAppInfoBootstrap, bootstrap.NewLogBootstrap,
		bootstrap.NewMetricsBootstrap, bootstrap.NewTracingBootstrap,
		bootstrap.NewInfrastructureBootstrap, bootstrap.NewSpec,
		componentsBoot, bootstrap.NewServerBootstrap, bootstrap.NewJobBootstrap, bootstrap.NewRuntimeBootstrap,
		bootstrap.NewApplicationBootstrap, bootstrap.NewKratosApp,
	)
	return nil, nil, nil
}

func newRegistrar() registry.Registrar { return nil }

func initializeDrivers(info appinfo.AppInfo, spec *bootstrap.Spec) (*driverAssembly, func(), error) {
	wire.Build(bootstrap.DriverProviderSet, componentsBoot, wire.Struct(new(driverAssembly), "*"))
	return nil, nil, nil
}
