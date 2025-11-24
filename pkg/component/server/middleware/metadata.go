package middleware

import (
	"context"
	"net/url"
	"strings"

	"github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
)

func Metadata(prefix []string) middleware.Middleware {
	var prefixLen = len(prefix)
	return func(handler middleware.Handler) middleware.Handler {
		// 如果没有配置，则跳过中间件
		if prefixLen == 0 {
			return handler
		}
		return func(ctx context.Context, req any) (any, error) {
			// 从ctx中获取 Transporter
			tr, ok := transport.FromServerContext(ctx)
			if !ok {
				return handler(ctx, req)
			}

			// 读取符合前缀的header添加到md里
			md := metadata.Metadata{}
			header := tr.RequestHeader()
			for _, k := range header.Keys() {
				for _, p := range prefix {
					if strings.HasPrefix(strings.ToLower(k), p) {
						for _, v := range header.Values(k) {
							vv, _ := url.QueryUnescape(v)
							md.Add(k, vv)
						}
						break // 命中一个，就跳出循环
					}
				}
			}

			// 添加到ctx中
			ctx = metadata.NewServerContext(ctx, md)
			return handler(ctx, req)
		}
	}
}
