package job

import (
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/config"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/cron"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/otel"
)

var ProviderSet = wire.NewSet(
	config.NewConfig,
	cron.NewCron,
	cron.NewCronLogger,
	cron.NewScheduleParser,
	otel.NewMetricsProvider,
	otel.NewTracingProvider,
	NewRegister,
	NewServer,
)
