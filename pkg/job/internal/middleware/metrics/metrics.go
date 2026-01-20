package metrics

import (
	"context"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/otel"
)

func Middleware(provider otel.MetricsProvider) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context) (err error) {
			st := time.Now()
			provider.ReportStart(ctx)
			defer func() {
				provider.ReportDone(ctx, err, time.Since(st))
			}()
			err = next(ctx)
			return
		}
	}
}
