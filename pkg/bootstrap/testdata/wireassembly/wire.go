//go:build wireinject

package wireassembly

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

func initialize(ctx context.Context, sources config.Sources, version string, stopDelay time.Duration) (*assembly, func(), error) {
	wire.Build(
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

func initializeComponents(ctx context.Context, sources config.Sources, version string) (*kratos.App, func(), error) {
	wire.Build(
		config.NewManager, bootstrap.ApplicationSpec, app.NewConfig, bootstrap.NewStopPolicy,
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
