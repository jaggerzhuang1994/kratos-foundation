// Package server 提供服务器中间件的创建和配置功能
//
// 该文件定义了中间件链的构建逻辑，按照最佳实践组装所有中间件。
// 中间件按照以下顺序执行（从外到内）：
//  1. Recovery - 异常恢复
//  2. Timeout - 超时控制
//  3. Metadata - 元数据注入
//  4. Tracing - 链路追踪
//  5. Metrics - 监控指标
//  6. Logging - 日志记录
//  7. Validator - 参数验证
//  8. RateLimit - 限流控制
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

// Middlewares 中间件链的类型别名
//
// 该类型是一个中间件切片的包装，提供了链式调用的能力
type Middlewares = SliceT[middleware.Middleware]

// middlewares 中间件链的内部实现
type middlewares = sliceT[middleware.Middleware]

// NewMiddlewares 创建并配置服务器中间件链
//
// 该函数按照最佳实践组装所有中间件，每个中间件都有特定的职责：
//
// 中间件顺序（执行顺序从外到内）：
//  1. Recovery: 捕获 panic，防止服务崩溃
//  2. Timeout: 控制请求超时时间
//  3. Metadata: 将 HTTP/gRPC 元数据注入到上下文
//  4. Tracing: 分布式链路追踪
//  5. Metrics: Prometheus 监控指标收集
//  6. Logging: 结构化日志记录
//  7. Validator: 请求参数验证
//  8. RateLimit: 请求限流控制
//
// 参数说明：
//   - config: 中间件配置（超时、日志、追踪、指标等）
//   - log: 日志记录器
//   - metrics_: 指标收集器
//   - tracing_: 链路追踪器
//
// 返回：
//   - Middlewares: 包含所有中间件的链，按正确顺序组织
//
// 注意事项：
//   - 中间件顺序很重要，请勿随意更改
//   - 某些中间件可能为 nil（配置禁用或初始化失败）
//   - Metrics 中间件初始化失败只会记录警告，不会中断服务启动
func NewMiddlewares(
	config Config,
	log log.Log,
	metrics_ metrics.Metrics,
	tracing_ tracing.Tracing,
) Middlewares {
	var m middlewares
	conf := config.GetMiddleware()

	// ============================================================
	// 第 1 层：异常恢复中间件
	// ============================================================
	// 必须放在最外层，捕获所有后续中间件和业务逻辑中的 panic
	// 防止服务崩溃，返回友好的错误响应
	m.Add(recovery.Recovery())

	// ============================================================
	// 第 2 层：超时中间件
	// ============================================================
	// 控制请求的最大执行时间
	// 超时后自动取消请求，返回超时错误
	m.Add(timeout.Server(conf.GetTimeout()))

	// ============================================================
	// 第 3 层：元数据中间件
	// ============================================================
	// 将 HTTP/gRPC 元数据（如请求头、认证信息）注入到上下文
	// 后续处理器可以从上下文获取元数据
	if mm := metadata.Server(conf.GetMetadata()); mm != nil {
		m.Add(mm)
	}

	// ============================================================
	// 第 4 层：链路追踪中间件
	// ============================================================
	// 集成 OpenTelemetry 分布式追踪
	// 为每个请求创建 Span，记录调用链路
	if mm := tracing.Server(tracing_, conf.GetTracing()); mm != nil {
		m.Add(mm)
	}

	// ============================================================
	// 第 5 层：监控指标中间件
	// ============================================================
	// 收集 Prometheus 指标（请求率、错误率、延迟等）
	// 如果初始化失败，只记录警告，不影响服务启动
	{
		mm, err := metrics.Server(metrics_, conf.GetMetrics())
		if err != nil {
			log.With("error", err).Warn("failed to add metrics middleware")
		} else {
			m.Add(mm)
		}
	}

	// ============================================================
	// 第 6 层：日志中间件
	// ============================================================
	// 记录请求和响应的结构化日志
	// 包含请求路径、参数、状态码、耗时等信息
	if mm := logging.Server(log, conf.GetLogging()); mm != nil {
		m.Add(mm)
	}

	// ============================================================
	// 第 7 层：参数验证中间件
	// ============================================================
	// 自动验证请求参数（基于 validator 标签）
	// 验证失败返回 400 错误
	if mm := validator.Validator(conf.GetValidator()); mm != nil {
		m.Add(mm)
	}

	// ============================================================
	// 第 8 层：限流中间件
	// ============================================================
	// 基于令牌桶算法的请求限流
	// 防止服务过载，保护服务稳定性
	if mm := ratelimit.Server(conf.GetRateLimit()); mm != nil {
		m.Add(mm)
	}

	return &m
}
