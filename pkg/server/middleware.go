package server

import (
	"cmp"
	"slices"

	"github.com/go-kratos/kratos/v2/middleware"
	deadlinemiddleware "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/deadline"
)

const (
	// MiddlewarePriorityRecovery 让 panic 恢复位于最外层，确保后续中间件异常可被捕获。
	MiddlewarePriorityRecovery = 100
	// MiddlewarePriorityDeadline 在恢复层之后统一约束请求生命周期。
	MiddlewarePriorityDeadline = 200
	// MiddlewarePriorityRequestDebug 在观测与业务处理前恢复已授权的请求诊断标记。
	MiddlewarePriorityRequestDebug = 250
	// MiddlewarePriorityMetadata 在观测逻辑前完成传输元数据导入。
	MiddlewarePriorityMetadata = 300
	// MiddlewarePriorityTracing 让后续处理都处于服务端 span 内。
	MiddlewarePriorityTracing = 400
	// MiddlewarePriorityMetrics 在 tracing 后记录请求指标。
	MiddlewarePriorityMetrics = 500
	// MiddlewarePriorityLogging 在上下文信息完整后记录请求日志。
	MiddlewarePriorityLogging = 600
	// MiddlewarePriorityCustom 是业务中间件的默认插入位置。
	MiddlewarePriorityCustom = 650
	// MiddlewarePriorityValidate 在进入业务逻辑前校验请求。
	MiddlewarePriorityValidate = 700
	// MiddlewarePriorityLimit 最靠近业务入口执行限流和熔断。
	MiddlewarePriorityLimit = 800
)

// MiddlewareSpec 用名称和优先级描述中间件；同名业务配置会替换默认实现，便于定点
// 定制而不用复制整条中间件链。
type MiddlewareSpec struct {
	Name       string
	Priority   int
	Middleware middleware.Middleware
}

type middlewareSet []MiddlewareSpec

// newMiddlewares 组装常驻中间件链。支持热更新的中间件始终留在链上，由各自策略在
// 请求期决定执行实现还是直接透传，因此这里不再按配置决定是否加入——否则运行期
// 打开一个原本禁用的中间件就必须重建 Server。
func newMiddlewares(policies *middlewarePolicies) middlewareSet {
	return middlewareSet{
		{
			Name:       "recovery",
			Priority:   MiddlewarePriorityRecovery,
			Middleware: recoverRequests(policies.logger),
		},
		{
			Name:       "deadline",
			Priority:   MiddlewarePriorityDeadline,
			Middleware: deadlinemiddleware.Server(policies.deadline),
		},
		{Name: "request_debug", Priority: MiddlewarePriorityRequestDebug, Middleware: policies.requestDebug.Middleware()},
		{
			Name:       "metadata",
			Priority:   MiddlewarePriorityMetadata,
			Middleware: policies.metadata.Middleware(),
		},
		{
			Name:       "tracing",
			Priority:   MiddlewarePriorityTracing,
			Middleware: policies.tracing.Middleware(),
		},
		{
			Name:       "metrics",
			Priority:   MiddlewarePriorityMetrics,
			Middleware: policies.metrics.Middleware(),
		},
		{
			Name:       "errors",
			Priority:   MiddlewarePriorityMetrics + 50,
			Middleware: normalizeErrors(policies.logger),
		},
		{
			Name:       "logging",
			Priority:   MiddlewarePriorityLogging,
			Middleware: policies.logging.Middleware(),
		},
		{
			Name:       "validator",
			Priority:   MiddlewarePriorityValidate,
			Middleware: policies.validator.Middleware(),
		},
		{
			Name:       "ratelimit",
			Priority:   MiddlewarePriorityLimit,
			Middleware: policies.rateLimit.Middleware(),
		},
	}
}

// build 允许业务按名称替换默认项，避免复制整条中间件链。
func (m middlewareSet) build(custom []MiddlewareSpec) []middleware.Middleware {
	specs := append(middlewareSet(nil), m...)
	for _, candidate := range custom {
		if candidate.Name != "" {
			filtered := specs[:0]
			for _, existing := range specs {
				if existing.Name != candidate.Name {
					filtered = append(filtered, existing)
				}
			}
			specs = filtered
		}
		if candidate.Middleware != nil {
			specs = append(specs, candidate)
		}
	}
	slices.SortStableFunc(specs, func(a, b MiddlewareSpec) int {
		return cmp.Compare(a.Priority, b.Priority)
	})

	result := make([]middleware.Middleware, 0, len(specs))
	for _, spec := range specs {
		if spec.Middleware != nil {
			result = append(result, spec.Middleware)
		}
	}
	return result
}
