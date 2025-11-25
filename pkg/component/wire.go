package component

import (
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/app"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/database"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/metric"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/server"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/tracing"
)

var ProviderSet = wire.NewSet(
	app.ProviderSet,
	database.ProviderSet,
	log.ProviderSet,
	metric.ProviderSet,
	redis.ProviderSet,
	server.ProviderSet,
	tracing.ProviderSet,
)

var _ = ProviderSet
