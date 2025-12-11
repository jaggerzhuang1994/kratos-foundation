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
	"go.uber.org/zap"
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
	var list []string

	var config = cfg.GetMiddleware()
	log = log.WithModule("server", cfg.GetLog())

	// 异常恢复
	m = append(m, recovery.Recovery())
	list = append(list, "recovery")

	// 超时中间件
	m = append(m, timeout.Server(
		log,
		config.GetTimeout(),
	))
	list = append(list, "timeout")

	// 往ctx中注入server metadata
	if metadata.Enable(config.GetMetadata()) {
		m = append(m, metadata.Server(config.GetMetadata()))
		list = append(list, "metadata")
	}

	// 链路追踪中间件
	if tracing.Enable(config.GetTracing()) {
		m = append(m, tracing.Server(tracing_, config.GetTracing()))
		list = append(list, "tracing")
	}

	// 监控中间件
	if metrics.Enable(config.GetMetrics()) {
		metricsMiddleware, err := metrics.Server(metrics_, config.GetMetrics())
		if err != nil {
			log.Warn("Failed to create metrics middleware", zap.Error(err))
		} else {
			m = append(m, metricsMiddleware)
			list = append(list, "metrics")
		}
	}

	// 日志中间件
	if logging.Enable(config.GetLogging()) {
		m = append(m, logging.Server(log, config.GetLogging()))
		list = append(list, "logging")
	}

	// 表单验证中间件
	if validator.Enable(config.GetValidator()) {
		m = append(m, validator.Validator())
		list = append(list, "validator")
	}

	// 限流
	if ratelimit.Enable(config.GetRatelimit()) {
		m = append(m, ratelimit.Server(config.GetRatelimit()))
		list = append(list, "ratelimit")
	}

	log.Debugf("server default middlewares %v", list)
	return m
}
