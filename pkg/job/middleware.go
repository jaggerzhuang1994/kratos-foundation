package job

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/middleware/logging"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/middleware/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/middleware/recovery"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/otel"
)

type Middlewares []middleware.Middleware

func NewMiddleware(
	log Log,
	tp otel.TracingProvider,
	mp otel.MetricsProvider,
) Middlewares {
	recoveryMiddleware := recovery.Middleware(log)
	restMiddleware := []middleware.Middleware{
		tracing.Middleware(tp),
		metrics.Middleware(mp),
		logging.Middleware(log),
	}
	return append([]middleware.Middleware{
		recoveryMiddleware,
	}, restMiddleware...)
}
