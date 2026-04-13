package metadata

import (
	"context"
	"net/url"

	"github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/gorilla/websocket"
)

func server(opts ...Option) middleware.Middleware {
	opt := &options{
		prefix: []string{"x-md-"}, // x-md-global-, x-md-local
	}
	for _, o := range opts {
		o(opt)
	}
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (reply any, err error) {
			tr, ok := transport.FromServerContext(ctx)
			if !ok {
				return handler(ctx, req)
			}

			md := opt.md.Clone()
			header := tr.RequestHeader()
			for _, k := range header.Keys() {
				if opt.hasPrefix(k) {
					for _, v := range header.Values(k) {
						vv, _ := url.QueryUnescape(v)
						md.Add(k, vv)
					}
				}
			}

			// 如果是ws请求，从query/子协议中读取header
			request, ok := http.RequestFromServerContext(ctx)
			if ok && websocket.IsWebSocketUpgrade(request) {
				// 从query解析md
				var queryValues = make(url.Values)
				if request.URL != nil {
					queryValues, _ = url.ParseQuery(request.URL.RawQuery)
				}
				for k := range queryValues {
					if opt.hasPrefix(k) {
						for _, v := range queryValues[k] {
							vv, _ := url.QueryUnescape(v)
							md.Add(k, vv)
						}
					}
				}
				// 从子协议解析md
				sp := websocket.Subprotocols(request)
				for i := 0; i < len(sp); i++ {
					if opt.hasPrefix(sp[i]) && i+1 < len(sp) {
						vv, _ := url.QueryUnescape(sp[i+1])
						md.Add(sp[i], vv)
						i++
					}
				}
			}

			ctx = metadata.NewServerContext(ctx, md)
			return handler(ctx, req)
		}
	}
}
