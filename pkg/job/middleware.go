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

// errJobPanicked 是观测层在下层未正常返回时记录的失败标记。
// 这里只识别异常退出，不调用 recover；具体 panic 与堆栈由最外层统一转换。
var errJobPanicked = errors.New("job execution panicked")

// newMiddlewares 按 tracing、metrics、logging 的顺序构造观测链。
// recovery 由 newManagedJob 固定在整个执行链最外层，不属于可选观测能力。
func newMiddlewares(
	log moduleLog,
	options managerOptions,
	tracingProvider foundationtracing.Provider,
	metricsProvider foundationmetrics.Provider,
) (middlewareChain, error) {
	middlewares, _, err := newMiddlewaresWithMetrics(log, options, tracingProvider, metricsProvider)
	return middlewares, err
}

func newMiddlewaresWithMetrics(
	log moduleLog,
	options managerOptions,
	tracingProvider foundationtracing.Provider,
	metricsProvider foundationmetrics.Provider,
) (middlewareChain, jobMetrics, error) {
	tp := newJobTracingProvider(
		tracingProvider,
		options.TracingEnabled,
	)
	mp, err := newJobMetricsProvider(
		metricsProvider,
		options.MetricsEnabled,
	)
	if err != nil {
		return nil, nil, err
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
	return middlewares, mp, nil
}

func loggingMiddleware(log moduleLog) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context) (err error) {
			// return next(ctx) 只有正常返回才会覆盖 err；panic 展开时保留失败标记。
			err = errJobPanicked
			started := time.Now()
			logger := log.WithContext(ctx)
			logger.With("event", "job.execution.started").Info("job.execution.started")
			defer func() {
				result := "failure"
				if err == nil {
					result = "success"
				} else if stoppedByContext(ctx, err) {
					result = "stopped"
				}
				// 失败详情仍只交给最终 ErrorHandler，结束事件保持稳定且不泄漏业务错误。
				logger.With("event", "job.execution.finished", "result", result, "duration", time.Since(started)).Info("job.execution.finished")
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
			err = errJobPanicked
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
			err = errJobPanicked
			ctx, span := provider.recordStart(ctx)
			defer func() { provider.recordEnd(span, err) }()
			return next(ctx)
		}
	}
}
