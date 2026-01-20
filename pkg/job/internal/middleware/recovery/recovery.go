package recovery

import (
	"context"
	"runtime/debug"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
)

func Middleware(log log.Log) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context) error {
			st := time.Now()
			defer func() {
				if r := recover(); r != nil {
					log.WithContext(ctx).With("duration", time.Since(st)).Errorf("job panic: %v\n%s", r, debug.Stack())
				}
			}()
			return next(ctx)
		}
	}
}
