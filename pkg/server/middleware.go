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
	log = log.WithModule("server/middleware", config.GetLog())

	var m Middlewares
	conf := config.GetMiddleware()

	// 异常恢复
	m = append(m, recovery.Recovery())
	log.Info("add recovery middleware")

	// 超时中间件
	m = append(m, timeout.Server(
		conf.GetTimeout(),
	))
	log.Info("add timeout middleware")

	// 往ctx中注入server metadata
	if mm := metadata.Server(conf.GetMetadata()); mm != nil {
		m = append(m, mm)
		log.Info("add metadata middleware")
	}

	// 链路追踪中间件
	if mm := tracing.Server(tracing_, conf.GetTracing()); mm != nil {
		m = append(m, mm)
		log.Info("add tracing middleware")
	}

	// 监控中间件
	{
		mm, err := metrics.Server(metrics_, conf.GetMetrics())
		if err != nil {
			log.Warn("failed to add metrics middleware ", err)
		} else {
			m = append(m, mm)
			log.Info("add metrics middleware")
		}
	}

	// 日志中间件
	if mm := logging.Server(log, conf.GetLogging()); mm != nil {
		m = append(m, mm)
		log.Info("add logging middleware")
	}

	// 表单验证中间件
	if mm := validator.Validator(conf.GetValidator()); mm != nil {
		m = append(m, mm)
		log.Info("add validator middleware")
	}

	// 限流
	if mm := ratelimit.Server(conf.GetRateLimit()); mm != nil {
		m = append(m, mm)
		log.Info("add ratelimit middleware")
	}

	return m
}
