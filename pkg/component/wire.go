package component

import (
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/app"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/client"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/database"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/server"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/tracing"
)

var ProviderSet = wire.NewSet(
	app.ProviderSet,
	client.ProviderSet,
	database.ProviderSet,
	log.ProviderSet,
	metrics.ProviderSet,
	redis.ProviderSet,
	registry.ProviderSet,
	server.ProviderSet,
	tracing.ProviderSet,
)

var _ = ProviderSet
