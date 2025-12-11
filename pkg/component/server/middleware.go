package server

import (
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/internal/middleware/logging"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/internal/middleware/metadata"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/internal/middleware/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/internal/middleware/ratelimit"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/internal/middleware/timeout"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/internal/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/internal/middleware/validator"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
)

type Middleware = middleware.Middleware

type DefaultMiddleware []Middleware

func NewDefaultMiddleware(
	cfg *Config,
	log *log.Log,
	metrics_ *metrics.Metrics,
	tracing_ *tracing.Tracing,
) DefaultMiddleware {
	var m DefaultMiddleware

	var config = cfg.GetMiddleware()
	log = log.WithModule("server", cfg.GetLog())

	// 异常恢复
	m = append(m, recovery.Recovery())

	// 超时中间件
	m = append(m, timeout.Server(
		log,
		config.GetTimeout(),
	))

	// 往ctx中注入server metadata
	if metadata.Enable(config.GetMetadata()) {
		m = append(m, metadata.Server(config.GetMetadata()))
	}

	// 链路追踪中间件
	if tracing.Enable(config.GetTracing()) {
		m = append(m, tracing.Server(tracing_, config.GetTracing()))
	}

	// 监控中间件
	if metrics.Enable(config.GetMetrics()) {
		metricsMiddleware, err := metrics.Server(metrics_, config.GetMetrics())
		if err != nil {
			log.Warn("Failed to create metrics middleware ", err)
		} else {
			m = append(m, metricsMiddleware)
		}
	}

	// 日志中间件
	if logging.Enable(config.GetLogging()) {
		m = append(m, logging.Server(log, config.GetLogging()))
	}

	// 表单验证中间件
	if validator.Enable(config.GetValidator()) {
		m = append(m, validator.Validator())
	}

	// 限流
	if ratelimit.Enable(config.GetRatelimit()) {
		m = append(m, ratelimit.Server(config.GetRatelimit()))
	}

	return m
}
