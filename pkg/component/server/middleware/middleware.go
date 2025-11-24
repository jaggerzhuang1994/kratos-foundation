package middleware

import (
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/logging"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/metric"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
)

type Middleware = middleware.Middleware
type Handler = middleware.Handler

var Chain = middleware.Chain

type Middlewares []middleware.Middleware

type ServerMiddlewareConfig = kratos_foundation_pb.ServerComponentConfig_Server_Middleware

func NewServerMiddleware(
	log *log.Log,
	metrics *metric.Metrics,
	tracing *tracing.Tracing,
	commonMiddlewareCfg *ServerMiddlewareConfig,
	serverMiddlewareCfg *ServerMiddlewareConfig,
) Middlewares {
	var m Middlewares

	// 异常恢复
	m = append(m, recovery.Recovery())

	// 往ctx中注入server metadata
	if !utils.Select(commonMiddlewareCfg.GetMetadata().GetDisable(), serverMiddlewareCfg.GetMetadata().GetDisable()) {
		// 合并2个 prefix
		m = append(m, Metadata(append(commonMiddlewareCfg.GetMetadata().GetPrefix(), serverMiddlewareCfg.GetMetadata().GetPrefix()...)))
	}

	// 链路追踪中间件
	if !utils.Select(commonMiddlewareCfg.GetTracing().GetDisable(), serverMiddlewareCfg.GetTracing().GetDisable()) {
		m = append(m, Tracing(tracing, utils.Select(
			serverMiddlewareCfg.GetTracing().GetTracerName(),
			commonMiddlewareCfg.GetTracing().GetTracerName(),
			tracing.GetDefaultTracerName(),
		)))
	}

	// 监控中间件
	if !utils.Select(commonMiddlewareCfg.GetMetrics().GetDisable(), serverMiddlewareCfg.GetMetrics().GetDisable()) {
		// 监控中间件
		m = append(m, Metrics(log.NewHelper(), metrics, utils.Select(
			serverMiddlewareCfg.GetMetrics().GetMeterName(),
			commonMiddlewareCfg.GetMetrics().GetMeterName(),
			metrics.GetDefaultMeterName(),
		)))
	}

	// 日志中间件
	if !utils.Select(commonMiddlewareCfg.GetLogger().GetDisable(), serverMiddlewareCfg.GetLogger().GetDisable()) {
		m = append(m, logging.Server(log.GetLogger()))
	}

	// todo 限流
	// m = append(m, ratelimit.Server())

	// 表单验证中间件
	if !utils.Select(commonMiddlewareCfg.GetValidator().GetDisable(), serverMiddlewareCfg.GetValidator().GetDisable()) {
		m = append(m, Validator())
	}

	return m
}
