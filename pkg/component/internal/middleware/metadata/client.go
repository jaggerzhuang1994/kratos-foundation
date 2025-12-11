package metadata

import (
	"context"

	"github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
)

// Client is middleware client-side metadata.
func client(opts ...Option) middleware.Middleware {
	opt := &options{
		prefix: []string{"x-md-global-"},
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
			// x-md-local-
			for k, vList := range opt.md {
				for _, v := range vList {
					header.Add(k, v)
				}
			}
			if md, ok := metadata.FromClientContext(ctx); ok {
				for k, vList := range md {
					for _, v := range vList {
						header.Add(k, v)
					}
				}
			}
			// x-md-global-
			if md, ok := metadata.FromServerContext(ctx); ok {
				for k, vList := range md {
					if opt.hasPrefix(k) {
						for _, v := range vList {
							header.Add(k, v)
						}
					}
				}
			}
			return handler(ctx, req)
		}
	}
}
