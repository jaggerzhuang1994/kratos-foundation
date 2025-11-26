package server

import (
	"context"
	"regexp"
	"time"

	"github.com/armon/go-radix"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/pkg/errors"
)

type RouteTimeoutMiddleware middleware.Middleware

func NewRouteTimeoutMiddleware(config *Config) (RouteTimeoutMiddleware, error) {
	if len(config.GetTimeout()) == 0 {
		// 没有规则
		return nil, nil
	}

	// path用hash
	pathHash := map[string]time.Duration{}
	// 前缀用 github.com/armon/go-radix
	tree := radix.New()
	// 正则则预编译
	regexps := make([]struct {
		*regexp.Regexp
		time.Duration
	}, 0)

	for i, cfg := range config.GetTimeout() {
		if cfg.GetTimeout().AsDuration() <= 0 {
			return nil, errors.Errorf("invalid timeout[%d]=%s", i, cfg.GetTimeout().String())
		}
		if cfg.GetPath() != "" {
			pathHash[cfg.GetPath()] = cfg.GetTimeout().AsDuration()
		}
		if cfg.GetPrefix() != "" {
			tree.Insert(cfg.GetPrefix(), cfg.GetTimeout().AsDuration())
		}
		if cfg.GetRegexp() != "" {
			r, err := regexp.Compile(cfg.GetRegexp())
			if err != nil {
				return nil, errors.WithMessagef(err, "invalid timeout[%d].regexp=%s", i, cfg.GetRegexp())
			}
			regexps = append(regexps, struct {
				*regexp.Regexp
				time.Duration
			}{Regexp: r, Duration: cfg.GetTimeout().AsDuration()})
		}
	}

	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			var ok bool

			tr, ok := transport.FromServerContext(ctx)
			if !ok {
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
				} else { // 前缀没查到，查正则
					for _, r := range regexps {
						if r.FindString(operation) == operation {
							timeout = r.Duration
							match = true
							break
						}
					}
				}
			}

			if !match {
				return handler(ctx, req)
			}

			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			return handler(ctx, req)
		}
	}, nil
}
