package job

import (
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/otel"
)

var ProviderSet = wire.NewSet(
	NewConfig,
	NewDefaultConfig,
	NewLog,
	NewCronLog,
	NewCron,
	NewCronLogger,
	NewScheduleParser,
	NewMiddleware,
	otel.NewMetricsProvider,
	otel.NewTracingProvider,
	NewRegister,
	NewServer,
)
