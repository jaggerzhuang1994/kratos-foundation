//go:build wireinject

package main

import (
	"github.com/go-kratos/kratos/v2"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

func initialize(path configPath, version string) (*kratos.App, func(), error) {
	wire.Build(
		newSpec, app.NewRegistrar, registry.NewFactory, newGreetingService, boot,
		log.NewLogger, wire.Bind(new(kratoslog.Logger), new(log.Logger)),
		bootstrap.NewConfigManager, appinfo.New, app.NewConfig,
		metrics.NewProvider, metrics.NewMetrics, tracing.NewProvider,
		bootstrap.ApplicationSpec,
		bootstrap.NewAppInfoBootstrap, bootstrap.NewLogBootstrap,
		bootstrap.NewMetricsBootstrap, bootstrap.NewTracingBootstrap,
		bootstrap.NewInfrastructureBootstrap,
		job.DefaultCoordinator, bootstrap.NewServerBootstrap, bootstrap.NewJobBootstrap, bootstrap.NewRuntimeBootstrap,
		app.NewStopPolicy, bootstrap.NewApplicationBootstrap, bootstrap.NewKratosApp,
	)
	return nil, nil, nil
}
