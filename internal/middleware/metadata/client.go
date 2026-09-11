package metadata

import (
	"context"
	"net/url"

	"github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
)

// client 创建客户端实现并把上下文元数据写入下游请求。
func client(opts ...option) middleware.Middleware {
	opt := &options{
		prefix: []string{"x-md-"},
	}
	for _, o := range opts {
		o(opt)
	}
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (reply any, err error) {
			tr, ok := transport.FromClientContext(ctx)
			if !ok {
				return handler(ctx, req)
			}

			header := tr.RequestHeader()
			add := func(key, value string) {
				// 保留的 metadata 不透传
				if isReservedMetadataKey(key) {
					return
				}
				value = url.QueryEscape(value)
				// HTTP 与 gRPC 均通过各自 Transport 的 Header 适配器写入。
				header.Add(key, value)
			}
			// 常量先写入，调用方显式上下文随后追加，便于网关保留完整来源链。
			for k, vList := range opt.md {
				for _, v := range vList {
					add(k, v)
				}
			}
			// 运行时调用 metadata.NewClientContext 显示传入的md 明确要透传给client的md
			if md, ok := metadata.FromClientContext(ctx); ok {
				for k, vList := range md {
					for _, v := range vList {
						add(k, v)
					}
				}
			}
			// 服务端上下文只透传白名单前缀，避免把上游私有头无意扩散到下游。
			if md, ok := metadata.FromServerContext(ctx); ok {
				for k, vList := range md {
					if opt.hasPrefix(k) {
						for _, v := range vList {
							add(k, v)
						}
					}
				}
			}

			return handler(ctx, req)
		}
	}
}
