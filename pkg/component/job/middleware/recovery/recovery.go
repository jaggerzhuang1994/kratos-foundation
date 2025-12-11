package recovery

import (
	"context"
	"runtime"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
)

func Middleware(log *log.Log) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context) error {
			st := time.Now()
			defer func() {
				if r := recover(); r != nil {
					buf := make([]byte, 64<<10) //nolint:mnd
					n := runtime.Stack(buf, false)
					buf = buf[:n]
					log.WithContext(ctx).With("duration", time.Since(st)).Errorf("%v%s\n", r, buf)
				}
			}()
			return next(ctx)
		}
	}
}
