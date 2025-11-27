package timeout

import (
	"context"
	"time"

	"github.com/armon/go-radix"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
)

type Config = kratos_foundation_pb.MiddlewareConfig_Timeout

func Server(log *log.Helper, config *Config) middleware.Middleware {
	return makeMiddleware(log, transport.FromServerContext, time.Second, config)
}

func Client(log *log.Helper, config *Config) middleware.Middleware {
	return makeMiddleware(log, transport.FromClientContext, 2*time.Second, config)
}

func makeMiddleware(
	log *log.Helper,
	trExporter func(context.Context) (transport.Transporter, bool),
	defaultTimeout time.Duration,
	config *Config,
) middleware.Middleware {
	rules := config.GetRoutes()
	if config.GetDefault().AsDuration() > 0 {
		defaultTimeout = config.GetDefault().AsDuration()
	}

	// path用map
	pathHash := map[string]time.Duration{}
	// 前缀用 github.com/armon/go-radix
	tree := radix.New()

	hasRule := false

	// 遍历配置，构建 hash / tree
	for _, rule := range rules {
		timeout := rule.GetTimeout().AsDuration()
		if timeout <= 0 {
			log.Warnf("route_timeout middleware has invalid timeout=%s, ignore it", rule.GetTimeout().String())
			continue
		}
		if rule.GetPath() != "" {
			pathHash[rule.GetPath()] = timeout
			hasRule = true
		}
		if rule.GetPrefix() != "" {
			tree.Insert(rule.GetPrefix(), timeout)
			hasRule = true
		}
	}

	if !hasRule {
		// 没有规则，使用默认超时时间设置一个中间件
		return func(handler middleware.Handler) middleware.Handler {
			return func(ctx context.Context, req any) (any, error) {
				ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
				defer cancel()
				return handler(ctx, req)
			}
		}
	}

	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			var ok bool
			tr, ok := trExporter(ctx)
			if !ok {
				ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
				defer cancel()
				return handler(ctx, req)
			}
			operation := tr.Operation()
			var timeout time.Duration
			var match bool

			// 优先匹配 hash
			timeout, match = pathHash[operation]
			if !match { // 不存在hash则根据前缀树查
				var v any
				_, v, match = tree.LongestPrefix(operation)
				if match { // 查到前缀匹配
					timeout = v.(time.Duration)
				}
			}

			// 没有匹配项，则跳过
			if !match {
				ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
				defer cancel()
				return handler(ctx, req)
			}

			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			return handler(ctx, req)
		}
	}
}
