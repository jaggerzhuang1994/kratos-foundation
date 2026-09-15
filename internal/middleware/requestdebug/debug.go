// Package requestdebug 在传输边界编解码应用级 debug 状态。
package requestdebug

import (
	"context"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/request"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

// Header 是 HTTP 与 gRPC 共用的框架保留键；只有单个值 1 表示开启。
const Header = "x-foundation-debug"

// Server 默认恢复上游请求标记，显式 accept_incoming=false 时关闭接收。
func Server(config *config_pb.Middleware_RequestDebug) middleware.Middleware {
	if config != nil && config.AcceptIncoming != nil && !config.GetAcceptIncoming() {
		return nil
	}
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			if tr, ok := transport.FromServerContext(ctx); ok {
				values := tr.RequestHeader().Values(Header)
				// 多值、空值和非约定值均不采纳，避免代理拼接与不同解析器产生歧义。
				if len(values) == 1 && values[0] == "1" {
					ctx = request.WithDebug(ctx)
				}
			}
			return next(ctx, req)
		}
	}
}

// Client 根据请求状态写入传输头，原始 metadata 不具有开启 debug 的权限。
func Client(config *config_pb.Middleware_RequestDebug) middleware.Middleware {
	propagate := config == nil || config.Propagate == nil || config.GetPropagate()
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			tr, ok := transport.FromClientContext(ctx)
			if !ok {
				return next(ctx, req)
			}
			if tr.Kind() == transport.KindGRPC {
				// Kratos 最终会向原生 outgoing metadata 追加头；先移除旧值，避免伪造或重复。
				ctx = clearOutgoingDebug(ctx)
			}
			header := tr.RequestHeader()
			value := ""
			if propagate && request.IsDebug(ctx) {
				value = "1"
			}
			if value != "" || len(header.Values(Header)) != 0 {
				// Header 接口没有 Delete；空值使已有标记失效，普通未标记请求不新增头。
				header.Set(Header, value)
			}
			return next(ctx, req)
		}
	}
}
