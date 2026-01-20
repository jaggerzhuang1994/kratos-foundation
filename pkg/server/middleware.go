package server

import (
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/jaggerzhuang1994/kratos-foundation/internal/middleware/logging"
	"github.com/jaggerzhuang1994/kratos-foundation/internal/middleware/metadata"
	"github.com/jaggerzhuang1994/kratos-foundation/internal/middleware/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/internal/middleware/ratelimit"
	"github.com/jaggerzhuang1994/kratos-foundation/internal/middleware/timeout"
	"github.com/jaggerzhuang1994/kratos-foundation/internal/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/internal/middleware/validator"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
)

type Middleware = middleware.Middleware
type Middlewares []Middleware

func NewMiddlewares(
	config Config,
	log log.Log,
	metrics_ metrics.Metrics,
	tracing_ tracing.Tracing,
) Middlewares {
	var m Middlewares
	conf := config.GetMiddleware()

	// 异常恢复
	m = append(m, recovery.Recovery())

	// 超时中间件
	m = append(m, timeout.Server(
		conf.GetTimeout(),
	))

	// 往ctx中注入server metadata
	m = append(m, metadata.Server(conf.GetMetadata()))

	// 链路追踪中间件
	m = append(m, tracing.Server(tracing_, conf.GetTracing()))

	// 监控中间件
	mm, err := metrics.Server(metrics_, conf.GetMetrics())
	if err != nil {
		log.Warn("Failed to create metrics middleware ", err)
	} else {
		m = append(m, mm)
	}

	// 日志中间件
	m = append(m, logging.Server(log, conf.GetLogging()))

	// 表单验证中间件
	m = append(m, validator.Validator(conf.GetValidator()))

	// 限流
	m = append(m, ratelimit.Server(conf.GetRateLimit()))

	return m
}
