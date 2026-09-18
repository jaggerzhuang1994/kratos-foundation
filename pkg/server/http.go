package server

import (
	"context"
	"fmt"
	nethttp "net/http"
	"net/url"

	"github.com/go-kratos/kratos/v2/transport/http"
	foundationhttp "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// HTTPServer 是 Foundation endpoint 使用的 Kratos HTTP Server。
type HTTPServer = *http.Server

// HTTPHandler 处理一条自定义 HTTP 路由；返回值由服务器配置的响应或错误编码器输出。
// Request Context 包含服务端中间件派生状态。
type HTTPHandler func(*nethttp.Request) (any, error)

// HTTPWriterHandler 处理需要直接控制状态码、Header 或响应体的自定义 HTTP 路由。
// Request Context 包含服务端中间件派生状态。
type HTTPWriterHandler func(nethttp.ResponseWriter, *nethttp.Request) error

// HandleHTTP 创建一条自定义 HTTP 路由注册回调。
// 它自动执行服务端中间件链，并统一编码 handler 返回值。
func HandleHTTP(method, path string, handler HTTPHandler) HTTPEndpoint {
	if handler == nil {
		return nil
	}
	return func(server HTTPServer) error {
		server.Route("/").Handle(method, path, middlewareHTTPHandler(path, handler))
		return nil
	}
}

// HandleHTTPWriter 创建一条允许 handler 直接写响应的自定义 HTTP 路由注册回调。
// 中间件提前返回的 reply 仍由服务器配置的响应编码器输出。
func HandleHTTPWriter(method, path string, handler HTTPWriterHandler) HTTPEndpoint {
	if handler == nil {
		return nil
	}
	return func(server HTTPServer) error {
		server.Route("/").Handle(method, path, middlewareHTTPWriterHandler(path, handler))
		return nil
	}
}

// middlewareHTTPHandler 让声明式自定义路由复用生成式 HTTP 接口的中间件和编码边界。
func middlewareHTTPHandler(operation string, handler HTTPHandler) http.HandlerFunc {
	return func(ctx http.Context) error {
		http.SetOperation(ctx, operation)
		next := ctx.Middleware(func(callCtx context.Context, req any) (any, error) {
			request, ok := req.(*nethttp.Request)
			if !ok || request == nil {
				return nil, fmt.Errorf("http middleware request has type %T, want *http.Request", req)
			}
			// 自定义处理器应观察中间件替换后的请求及其派生 Context。
			return handler(request.WithContext(callCtx))
		})
		reply, err := next(ctx, ctx.Request())
		return ctx.Returns(reply, err)
	}
}

// middlewareHTTPWriterHandler 保留中间件短路编码能力，
// 同时避免二次编码 handler 已写入的响应。
func middlewareHTTPWriterHandler(operation string, handler HTTPWriterHandler) http.HandlerFunc {
	return func(ctx http.Context) error {
		http.SetOperation(ctx, operation)
		next := ctx.Middleware(func(callCtx context.Context, req any) (any, error) {
			request, ok := req.(*nethttp.Request)
			if !ok || request == nil {
				return nil, fmt.Errorf("http middleware request has type %T, want *http.Request", req)
			}
			// Writer 模式仍接收中间件替换后的请求，但成功后由 handler 对响应负全责。
			return nil, handler(ctx.Response(), request.WithContext(callCtx))
		})
		reply, err := next(ctx, ctx.Request())
		if err != nil || reply == nil {
			return err
		}
		return ctx.Returns(reply, nil)
	}
}

// newHTTPServer 组装业务路由；监控端点由监听规划统一挂载。
func newHTTPServer(
	config componentConfig,
	opts httpServerOptions,
	spec *Spec,
	logger log.Logger,
	websockets *websocketHub,
) (HTTPServer, error) {
	if config.GetHttp().GetDisable() {
		return nil, nil
	}

	srv := http.NewServer(opts...)
	var websocket *websocketServer
	for _, registration := range spec.http.registrations {
		if registration.endpoint != nil {
			if err := registration.endpoint(srv); err != nil {
				return nil, fmt.Errorf("register HTTP endpoint: %w", err)
			}
			continue
		}
		if registration.websocket != nil {
			if websockets == nil {
				return nil, fmt.Errorf("websocket hub is nil")
			}
			if websocket == nil {
				websocket = newWebSocketServer(logger, srv, websockets)
			}
			endpoint := registration.websocket
			websocket.Handle(
				endpoint.path,
				endpoint.handler,
				endpoint.maxMessageBytes,
				endpoint.maxInFlightMessages,
				endpoint.upgrader...,
			)
		}
	}
	return srv, nil
}

// HTTPServerOptions 聚合构造 HTTPServer 所需的 Kratos option。
type httpServerOptions []http.ServerOption

// newHTTPServerOptions 把业务 option 放在基础配置之后，使显式配置拥有最终决定权。
func newHTTPServerOptions(
	config componentConfig,
	middlewares middlewareSet,
	spec *Spec,
) httpServerOptions {
	conf := config.GetHttp()
	var opts httpServerOptions
	if conf.GetNetwork() != "" {
		opts = append(opts, http.Network(conf.GetNetwork()))
	}
	if conf.GetAddr() != "" {
		opts = append(opts, http.Address(conf.GetAddr()))
	}
	if conf.GetEndpoint() != nil {
		opts = append(opts, http.Endpoint(&url.URL{
			Scheme: conf.GetEndpoint().GetScheme(),
			Host:   conf.GetEndpoint().GetHost(),
		}))
	}
	opts = append(opts, http.Timeout(0))
	if conf.GetDisableStrictSlash() {
		opts = append(opts, http.StrictSlash(false))
	}
	if conf.GetPathPrefix() != "" {
		opts = append(opts, http.PathPrefix(conf.GetPathPrefix()))
	}
	opts = append(opts, http.ErrorEncoder(foundationhttp.Encoder()))
	opts = append(opts, spec.http.options...)
	opts = append(opts, http.Middleware(middlewares.build(spec.http.middlewares)...))
	return opts
}
