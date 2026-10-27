package server

import (
	"context"
	"fmt"
	nethttp "net/http"

	"github.com/go-kratos/kratos/v2/transport/http"
)

// HTTPRequest 是 HandleRoute 处理器收到的请求视图：保留原始请求，并提供路径参数、查询参数与请求体绑定。
type HTTPRequest struct {
	// Request 是经过中间件替换后的原始请求，其 Context 与处理器的 ctx 相同。
	*nethttp.Request
	kratos http.Context
}

// PathParam 返回路由模板中的路径参数，如 "/orders/{id}" 的 id；不存在时返回空字符串。
func (r *HTTPRequest) PathParam(name string) string {
	values := r.kratos.Vars()[name]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// Query 返回查询参数的第一个值；不存在时返回空字符串。
func (r *HTTPRequest) Query(name string) string {
	return r.Request.URL.Query().Get(name)
}

// Bind 按 Content-Type 解码请求体到 v（JSON、Proto、Form 等，与生成式接口相同），受 server.http.request_body 上限约束。
func (r *HTTPRequest) Bind(v any) error {
	return r.kratos.Bind(v)
}

// BindQuery 把查询参数解码到 v。
func (r *HTTPRequest) BindQuery(v any) error {
	return r.kratos.BindQuery(v)
}

// HTTPRouteHandler 处理 HandleRoute 注册的路由；返回值由服务器配置的响应编码器输出，错误经服务端错误边界编码。
type HTTPRouteHandler func(ctx context.Context, request *HTTPRequest) (any, error)

// HandleRoute 注册一条经过 Foundation 服务端中间件链的手写 HTTP 路由，行为与 Protocol Buffers 生成的接口一致：
// 恢复 panic、截止时间、限流、元数据、访问日志、指标、链路与错误归一化全部生效。
// operation 是访问日志、指标与限流使用的接口名（如 "/orders.v1.Orders/Cancel"），空值使用 path 模板。
func HandleRoute(method, path, operation string, handler HTTPRouteHandler) HTTPEndpoint {
	if handler == nil {
		return nil
	}
	if operation == "" {
		operation = path
	}
	return func(server HTTPServer) error {
		server.Route("/").Handle(method, path, func(ctx http.Context) error {
			http.SetOperation(ctx, operation)
			next := ctx.Middleware(func(callCtx context.Context, req any) (any, error) {
				request, ok := req.(*nethttp.Request)
				if !ok || request == nil {
					return nil, fmt.Errorf("http middleware request has type %T, want *http.Request", req)
				}
				return handler(callCtx, &HTTPRequest{Request: request.WithContext(callCtx), kratos: ctx})
			})
			reply, err := next(ctx, ctx.Request())
			return ctx.Returns(reply, err)
		})
		return nil
	}
}
