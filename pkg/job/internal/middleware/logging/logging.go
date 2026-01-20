package logging

import (
	"context"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
)

func Middleware(log log.Log) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context) (err error) {
			st := time.Now()
			log = log.WithContext(ctx)
			log.Info("run")
			defer func() {
				if err == nil {
					log.With("duration", time.Since(st)).Info("done")
				} else {
					log.With("duration", time.Since(st)).Error("err done: ", err)
				}
			}()
			err = next(ctx)
			return
		}
	}
}
