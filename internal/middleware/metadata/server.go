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

// server 创建服务端实现并把传入值写入 Kratos 服务端元数据上下文。
func server(opts ...option) middleware.Middleware {
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
						vv, decodeErr := url.QueryUnescape(v)
						if decodeErr != nil {
							// 客户端编码器只会产生合法转义；畸形值来自非约定调用方，跳过比传播部分值安全。
							continue
						}
						md.Add(k, vv)
					}
				}
			}

			// WebSocket 握手无法可靠携带自定义头，因此兼容查询参数和子协议两种载体。
			request, ok := http.RequestFromServerContext(ctx)
			if ok && request != nil && websocket.IsWebSocketUpgrade(request) {
				// ParseQuery 已完成一次解码，再次 QueryUnescape 会把值中的字面量“+”误改为空格。
				var queryValues url.Values
				if request.URL != nil {
					var parseErr error
					queryValues, parseErr = url.ParseQuery(request.URL.RawQuery)
					if parseErr != nil {
						// ParseQuery 会在报错时返回部分结果；整组丢弃才能避免只透传一半身份信息。
						queryValues = nil
					}
				}
				for k := range queryValues {
					if opt.hasPrefix(k) {
						for _, v := range queryValues[k] {
							md.Add(k, v)
						}
					}
				}
				// 子协议使用相邻的“键、值”对，保持与现有客户端编码约定兼容。
				sp := websocket.Subprotocols(request)
				for i := 0; i < len(sp); i++ {
					if opt.hasPrefix(sp[i]) && i+1 < len(sp) {
						vv, decodeErr := url.QueryUnescape(sp[i+1])
						if decodeErr != nil {
							i++
							continue
						}
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
