package tracing

import (
	"context"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/otel"
)

func Middleware(provider *otel.TracingProvider) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context) (err error) {
			ctx, span := provider.RecordStart(ctx)
			defer func() {
				provider.RecordEnd(ctx, span, err)
			}()
			err = next(ctx)
			return
		}
	}
}
