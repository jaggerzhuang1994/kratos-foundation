package client

import (
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/internal/middleware/circuitbreaker"
	"github.com/jaggerzhuang1994/kratos-foundation/internal/middleware/logging"
	"github.com/jaggerzhuang1994/kratos-foundation/internal/middleware/metadata"
	"github.com/jaggerzhuang1994/kratos-foundation/internal/middleware/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/internal/middleware/timeout"
	"github.com/jaggerzhuang1994/kratos-foundation/internal/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
)

func (f *factory) newMiddleware(clientConfig ClientConfig) []middleware.Middleware {
	var m []middleware.Middleware

	config := clientConfig.Option.GetMiddleware()

	// 超时中间件
	m = append(m, timeout.Client(
		config.GetTimeout(),
	))

	// metadata
	m = append(m, metadata.Client(config.GetMetadata()))

	// tracing
	m = append(m, tracing.Client(f.tracing, config.GetTracing()))

	// 监控中间件
	mm, err := metrics.Client(f.metrics, config.GetMetrics())
	if err != nil {
		f.Error("Failed to create metrics middleware: ", err)
	} else {
		m = append(m, mm)
	}

	// 日志中间件
	m = append(m, logging.Client(f.log, config.GetLogging()))

	// 熔断器
	m = append(m, circuitbreaker.Client(config.GetCircuitBreaker()))

	// 过滤 nil
	m = utils.Filter(m, func(m middleware.Middleware) bool {
		return m != nil
	})
	return m
}
