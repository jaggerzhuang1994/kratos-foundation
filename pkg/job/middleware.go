package job

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	foundationmetrics "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	foundationtracing "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

// Handler 是任务中间件包装的可执行函数。
type Handler func(context.Context) error

// Middleware 为任务 Handler 增加横切能力。
type Middleware func(Handler) Handler

type jobNameKey struct{}

func withJobName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, jobNameKey{}, name)
}

// JobNameFromContext 返回当前任务的注册名称；非任务 Context 返回空字符串。
// 名称由任务执行器注入，可在任务实现和中间件中读取。
func JobNameFromContext(ctx context.Context) string {
	name, _ := ctx.Value(jobNameKey{}).(string)
	return name
}

func chainMiddlewares(middlewares ...Middleware) Middleware {
	return func(next Handler) Handler {
		for index := len(middlewares) - 1; index >= 0; index-- {
			if middlewares[index] != nil {
				next = middlewares[index](next)
			}
		}
		return next
	}
}

// middlewareChain 保留任务中间件的声明顺序。
type middlewareChain []Middleware

// newMiddlewares 统一确定 tracing、metrics、logging 和 recovery 的执行顺序，
// 避免不同任务拼出不一致的中间件链。
func newMiddlewares(
	log moduleLog,
	options managerOptions,
	tracingProvider foundationtracing.Provider,
	metricsProvider foundationmetrics.Provider,
) (middlewareChain, error) {
	tp := newJobTracingProvider(
		tracingProvider,
		options.TracingEnabled,
	)
	mp, err := newJobMetricsProvider(
		metricsProvider,
		options.MetricsEnabled,
	)
	if err != nil {
		return nil, err
	}
	var middlewares middlewareChain
	if options.TracingEnabled {
		middlewares = append(middlewares, tracingMiddleware(tp))
	}
	if options.MetricsEnabled {
		middlewares = append(middlewares, metricsMiddleware(mp))
	}
	if options.LoggingEnabled {
		middlewares = append(middlewares, loggingMiddleware(log))
	}
	middlewares = append(middlewares, recoveryMiddleware())
	return middlewares, nil
}

func loggingMiddleware(log moduleLog) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context) (err error) {
			started := time.Now()
			logger := log.WithContext(ctx)
			logger.Info("job execution started")
			defer func() {
				// 失败统一交给最终 ErrorHandler，避免重复输出错误和堆栈。
				switch {
				case err == nil:
					logger.With("duration", time.Since(started)).Info("job execution done")
				case ctx.Err() != nil && errors.Is(err, ctx.Err()):
					logger.With("duration", time.Since(started), "cause", ctx.Err()).Info("job execution stopped")
				}
			}()
			return next(ctx)
		}
	}
}

func recoveryMiddleware() Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context) (err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("job panic: %v\n%s", recovered, debug.Stack())
				}
			}()
			return next(ctx)
		}
	}
}

func metricsMiddleware(provider jobMetrics) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context) (err error) {
			started := time.Now()
			provider.reportStart(ctx)
			defer func() { provider.reportDone(ctx, err, time.Since(started)) }()
			return next(ctx)
		}
	}
}

func tracingMiddleware(provider jobTracing) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context) (err error) {
			ctx, span := provider.recordStart(ctx)
			defer func() { provider.recordEnd(span, err) }()
			return next(ctx)
		}
	}
}
