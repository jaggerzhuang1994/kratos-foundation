package server

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/deadline"
	serverlogging "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/logging"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/metadata"
	servermetrics "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/metrics"
	servertracing "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/tracing"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server/internal/middleware/ratelimit"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server/internal/middleware/validator"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
)

// middlewarePolicies 持有服务端全部可热更新的中间件策略，并且是 server.middleware
// 配置段的唯一订阅者。这里覆盖该段下的每一个字段，避免出现「字段所在配置段已被
// 订阅、但该字段实际不生效」这种只看日志无法察觉的情况；具体某次更新只重建其中
// 确实发生变化的项。
type middlewarePolicies struct {
	logger    log.Logger
	deadline  *deadline.Store
	metadata  *dynamicMiddleware
	tracing   *dynamicMiddleware
	metrics   *dynamicMiddleware
	logging   *dynamicMiddleware
	validator *dynamicMiddleware
	rateLimit *dynamicMiddleware

	// current 保存最近一次生效的配置，用于判断哪些中间件真正需要重建。
	current *config_pb.ServerMiddleware
}

// dynamicMiddlewares 汇总一次配置对应的全部中间件实现；字段为 nil 表示该项禁用。
type dynamicMiddlewares struct {
	metadata  middleware.Middleware
	tracing   middleware.Middleware
	metrics   middleware.Middleware
	logging   middleware.Middleware
	validator middleware.Middleware
	rateLimit middleware.Middleware
}

// newMiddlewarePolicies 构造全部中间件策略，并订阅 server 段以支持运行期更新。
func newMiddlewarePolicies(
	configManager foundationconfig.Manager,
	logger log.Logger,
	config componentConfig,
	metricsProvider metrics.Provider,
	tracingProvider tracing.Provider,
) (*middlewarePolicies, func(), error) {
	deadlineStore, err := deadline.NewStore(config.GetMiddleware().GetDeadline())
	if err != nil {
		return nil, nil, err
	}
	built, err := buildDynamicMiddlewares(
		logger,
		config.GetMiddleware(),
		metricsProvider,
		tracingProvider,
	)
	if err != nil {
		return nil, nil, err
	}
	policies := &middlewarePolicies{
		logger:    logger,
		deadline:  deadlineStore,
		metadata:  newDynamicMiddleware(built.metadata),
		tracing:   newDynamicMiddleware(built.tracing),
		metrics:   newDynamicMiddleware(built.metrics),
		logging:   newDynamicMiddleware(built.logging),
		validator: newDynamicMiddleware(built.validator),
		rateLimit: newDynamicMiddleware(built.rateLimit),
		current:   proto.CloneOf(config.GetMiddleware()),
	}

	// 只订阅 middleware 子树：监听整个 server 段会让修改监听地址之类的字段也触发
	// 中间件重建，进而重置 BBR 限流器的统计窗口。
	cancel, err := configManager.Subscribe(
		"server.middleware",
		new(config_pb.ServerMiddleware),
		func(_ string, value any, updateErr error) {
			next, valid := value.(*config_pb.ServerMiddleware)
			if updateErr == nil && (!valid || next == nil) {
				updateErr = fmt.Errorf(
					"server middleware config update has type %T, want *config_pb.ServerMiddleware",
					value,
				)
			}
			if updateErr == nil {
				updateErr = validateMiddlewareConfig(next)
			}
			// 订阅会回放当前快照；内容未变时不重建，也不误报配置更新。
			if updateErr == nil && proto.Equal(policies.current, next) {
				return
			}
			if updateErr == nil {
				updateErr = policies.update(
					logger,
					next,
					metricsProvider,
					tracingProvider,
				)
			}
			if updateErr != nil {
				logger.With("error", updateErr).Error(
					"server middleware config update rejected",
				)
				return
			}
			logger.Info("server middleware config updated")
		},
		defaultMiddlewareConfig,
	)
	if err != nil {
		return nil, nil, err
	}

	var once sync.Once
	return policies, func() { once.Do(cancel) }, nil
}

// update 只重建配置确实发生变化的中间件。
//
// 重建会丢弃中间件的内部状态——例如 BBR 限流器的统计窗口会被清空——因此不能因为
// 同一配置段内其他中间件的改动而牵连重建。可能失败的构造都排在替换之前，失败时保持
// 全部旧值；成功路径逐项替换，并发请求可能短暂看到新旧混合的组合，但各中间件彼此
// 独立，混合不影响任何一项自身的正确性。
//
// 只有订阅回调会调用它，因此 p.current 无需额外加锁。
func (p *middlewarePolicies) update(
	logger log.Logger,
	config *config_pb.ServerMiddleware,
	metricsProvider metrics.Provider,
	tracingProvider tracing.Provider,
) error {
	previous := p.current
	// 先构造所有变化项，包含 Aegis 限流器；任何构造失败都不能留下部分新状态。
	var built dynamicMiddlewares
	metadataChanged := !proto.Equal(previous.GetMetadata(), config.GetMetadata())
	tracingChanged := !proto.Equal(previous.GetTracing(), config.GetTracing())
	metricsChanged := !proto.Equal(previous.GetMetrics(), config.GetMetrics())
	loggingChanged := !proto.Equal(previous.GetLogging(), config.GetLogging())
	validatorChanged := !proto.Equal(previous.GetValidator(), config.GetValidator())
	rateLimitChanged := !proto.Equal(previous.GetRateLimit(), config.GetRateLimit())
	if metricsChanged {
		metricsMiddleware, err := servermetrics.Server(metricsProvider, config.GetMetrics())
		if err != nil {
			return fmt.Errorf("create server metrics middleware: %w", err)
		}
		built.metrics = metricsMiddleware
	}
	if metadataChanged {
		built.metadata = metadata.Server(config.GetMetadata())
	}
	if tracingChanged {
		built.tracing = servertracing.Server(tracingProvider, config.GetTracing())
	}
	if loggingChanged {
		built.logging = serverlogging.Server(logger, config.GetLogging())
	}
	if validatorChanged {
		built.validator = validator.Validator(config.GetValidator())
	}
	if rateLimitChanged {
		built.rateLimit = ratelimit.Server(config.GetRateLimit())
	}
	if !proto.Equal(previous.GetDeadline(), config.GetDeadline()) {
		if err := p.deadline.Update(config.GetDeadline()); err != nil {
			return err
		}
	}
	if metadataChanged {
		p.metadata.Set(built.metadata)
	}
	if tracingChanged {
		p.tracing.Set(built.tracing)
	}
	if metricsChanged {
		p.metrics.Set(built.metrics)
	}
	if loggingChanged {
		p.logging.Set(built.logging)
	}
	if validatorChanged {
		p.validator.Set(built.validator)
	}
	if rateLimitChanged {
		p.rateLimit.Set(built.rateLimit)
	}
	p.current = proto.CloneOf(config)
	return nil
}

// buildDynamicMiddlewares 按最新配置构造中间件实现。Provider 被禁用时对应实现为
// nil，因此遥测中间件能否工作仍由构造期注入的 Provider 决定，配置只负责开关。
func buildDynamicMiddlewares(
	logger log.Logger,
	conf *config_pb.ServerMiddleware,
	metricsProvider metrics.Provider,
	tracingProvider tracing.Provider,
) (dynamicMiddlewares, error) {
	metricsMiddleware, err := servermetrics.Server(metricsProvider, conf.GetMetrics())
	if err != nil {
		return dynamicMiddlewares{}, fmt.Errorf("create server metrics middleware: %w", err)
	}
	return dynamicMiddlewares{
		metadata:  metadata.Server(conf.GetMetadata()),
		tracing:   servertracing.Server(tracingProvider, conf.GetTracing()),
		metrics:   metricsMiddleware,
		logging:   serverlogging.Server(logger, conf.GetLogging()),
		validator: validator.Validator(conf.GetValidator()),
		rateLimit: ratelimit.Server(conf.GetRateLimit()),
	}, nil
}

// snapshot 固化一次配置对应的中间件实现；middleware 为 nil 表示该中间件当前禁用。
type snapshot struct {
	middleware middleware.Middleware
}

// dynamicMiddleware 持有可原子替换的中间件实现。
//
// 它交给 Server 的中间件会常驻调用链，由每次请求读取当前快照来决定执行实现还是
// 直接透传。这是热更新能够生效的前提：Kratos 的中间件链在 Server 构造时就已固定，
// 无法在运行期增删，因此「是否启用」必须下沉为运行期判断而不是构造期分支。
type dynamicMiddleware struct {
	current atomic.Pointer[snapshot]
}

// newDynamicMiddleware 用初始实现构造策略；mw 为 nil 表示初始状态为禁用。
func newDynamicMiddleware(mw middleware.Middleware) *dynamicMiddleware {
	policy := &dynamicMiddleware{}
	policy.Set(mw)
	return policy
}

// Set 原子替换中间件实现；mw 为 nil 表示禁用。
//
// 替换会丢弃实现内部的状态（例如限流器的统计窗口），因此调用方只应在该中间件的
// 配置确实发生变化时调用，不要因为同批更新里其他中间件变了就一并替换。
func (p *dynamicMiddleware) Set(mw middleware.Middleware) {
	p.current.Store(&snapshot{middleware: mw})
}

// wrapped 缓存某个快照针对固定 handler 包装出的结果。
type wrapped struct {
	source  *snapshot
	handler middleware.Handler
}

// Middleware 返回常驻调用链的中间件。
func (p *dynamicMiddleware) Middleware() middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		// handler 在 Server 构造时固定，因此把每个快照包装出的 Handler 缓存下来，
		// 使请求热路径只多一次原子读取。并发重建的结果彼此等价，无需加锁。
		var active atomic.Pointer[wrapped]
		return func(ctx context.Context, req any) (any, error) {
			current := p.current.Load()
			if current == nil || current.middleware == nil {
				return handler(ctx, req)
			}
			if cached := active.Load(); cached != nil && cached.source == current {
				return cached.handler(ctx, req)
			}
			next := &wrapped{
				source:  current,
				handler: current.middleware(handler),
			}
			active.Store(next)
			return next.handler(ctx, req)
		}
	}
}
