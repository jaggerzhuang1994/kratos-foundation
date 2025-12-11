package logging

import (
	"context"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
)

func Middleware(log *log.Log) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context) (err error) {
			st := time.Now()
			log = log.WithContext(ctx)
			log.With("now", st).Debug("run")
			defer func() {
				if err == nil {
					log.With("duration", time.Since(st)).Debug("done")
				} else {
					log.With("duration", time.Since(st)).Error("err done: ", err)
				}
			}()
			err = next(ctx)
			return
		}
	}
}
