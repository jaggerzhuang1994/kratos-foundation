//go:build wireinject && demo

package main

import (
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	_ "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/database/mysql"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/client"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

func initialize(path configPath, version string) (*app.App, func(), error) {
	wire.Build(
		newSpec, newShop, boot,
		app.NewRegistrar, registry.NewFactory, wire.Bind(new(client.DiscoveryResolver), new(*registry.Factory)), client.NewFactory,
		database.NewManager, redis.NewManager, kafka.NewClientFactory,
		log.NewLogger, wire.Bind(new(kratoslog.Logger), new(log.Logger)),
		bootstrap.NewConfigManager, appinfo.New, app.NewConfig,
		metrics.NewProvider, metrics.NewMetrics, metrics.NewPusher, tracing.NewProvider,
		app.NewSpec, server.NewSpec, job.NewSpec,
		bootstrap.NewAppInfoBootstrap, bootstrap.NewLogBootstrap,
		bootstrap.NewMetricsBootstrap, bootstrap.NewTracingBootstrap,
		bootstrap.NewInfrastructureBootstrap,
		bootstrap.NewServerBootstrap, bootstrap.NewJobBootstrap, bootstrap.NewRuntimeBootstrap,
		app.NewStopPolicy, bootstrap.NewApplicationBootstrap, bootstrap.NewApp,
	)
	return nil, nil, nil
}
