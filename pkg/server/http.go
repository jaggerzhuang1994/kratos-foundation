package server

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/transport/http"
	foundationhttp "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// HTTPServer 是 Foundation endpoint 使用的 Kratos HTTP Server。
//
// 在 Register 回调里直接调用它的原生方法（Route(...).GET/POST/Handle、HandleFunc、Handle、HandlePrefix）注册的路由
// 不经过 Foundation 服务端中间件链：没有 panic 恢复、截止时间、限流、访问日志、请求指标、链路与错误归一化，
// 除非处理器自行调用 ctx.Middleware。手写路由优先使用 HandleRoute（或 HandleHTTP / HandleHTTPWriter）。
type HTTPServer = *http.Server

// HTTPHandler 处理一条自定义 HTTP 路由；返回值由服务器配置的响应或错误编码器输出。
// Request Context 包含服务端中间件派生状态。
type HTTPHandler func(*nethttp.Request) (any, error)

// HTTPWriterHandler 处理需要直接控制状态码、Header 或响应体的自定义 HTTP 路由。
// Request Context 包含服务端中间件派生状态。
type HTTPWriterHandler func(nethttp.ResponseWriter, *nethttp.Request) error

// HandleHTTP 创建一条自定义 HTTP 路由注册回调。
// 它自动执行服务端中间件链，并统一编码 handler 返回值；operation 固定为 path 模板。
// 需要自定义 operation、读取路径参数或绑定请求体时使用 HandleRoute。
func HandleHTTP(method, path string, handler HTTPHandler) HTTPEndpoint {
	if handler == nil {
		return nil
	}
	return HandleRoute(method, path, "", func(_ context.Context, request *HTTPRequest) (any, error) {
		return handler(request.Request)
	})
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
) (*httpRuntime, error) {
	if config.GetHttp().GetDisable() {
		return nil, nil
	}

	conf := config.GetHttp()
	// Kratos 把未匹配路由交给 http.DefaultServeMux；net/http/pprof 被任何依赖导入时都会在其上注册剖析端点。
	// 业务端口保留 DefaultServeMux 回退，但屏蔽 /debug/pprof/，剖析只经 server.http.pprof 挂在管理端口。
	// 业务 option 在后，仍可显式覆盖。
	opts = append(httpServerOptions{http.NotFoundHandler(withoutPprof(nethttp.DefaultServeMux)), http.MethodNotAllowedHandler(withoutPprof(nethttp.DefaultServeMux))}, opts...)
	runtime := newManagedHTTPServer(conf.GetNetwork(), conf.GetAddr(), spec.http.listener, opts...)
	srv := runtime.HTTPServer
	// Kratos 构造的 http.Server 不设连接级超时；业务端口面向调用方，默认限制读取请求头与空闲连接时长。
	srv.ReadHeaderTimeout, srv.IdleTimeout = 10*time.Second, 120*time.Second
	if timeout := conf.GetReadHeaderTimeout(); timeout != nil {
		srv.ReadHeaderTimeout = timeout.AsDuration()
	}
	if timeout := conf.GetIdleTimeout(); timeout != nil {
		srv.IdleTimeout = timeout.AsDuration()
	}
	fail := func(err error) (*httpRuntime, error) {
		return nil, errors.Join(err, runtime.AbortStartup(context.Background()))
	}
	var websocket *websocketServer
	for _, registration := range spec.http.registrations {
		if registration.endpoint != nil {
			if err := registration.endpoint(srv); err != nil {
				return fail(fmt.Errorf("register HTTP endpoint: %w", err))
			}
			continue
		}
		if registration.websocket != nil {
			if websockets == nil {
				return fail(errors.New("websocket hub is nil"))
			}
			if websocket == nil {
				websocket = newWebSocketServer(logger, srv, websockets)
			}
			endpoint := registration.websocket
			websocket.Handle(
				endpoint.path,
				endpoint.operation(),
				endpoint.handler,
				endpoint.maxMessageBytes,
				endpoint.maxInFlightMessages,
				endpoint.upgrader...,
			)
		}
	}
	return runtime, nil
}

// HTTPServerOptions 聚合构造 HTTPServer 所需的 Kratos option。
type httpServerOptions []http.ServerOption

// newHTTPServerOptions 保留业务协议选项的覆盖顺序；监听选项由 Foundation 在构造时最终设置。
func newHTTPServerOptions(
	config componentConfig,
	middlewares middlewareSet,
	spec *Spec,
) httpServerOptions {
	conf := config.GetHttp()
	var opts httpServerOptions
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
	// 请求体上限在路由前生效，解码器把读取超限识别为 413；业务 option 在后仍可覆盖解码器。
	opts = append(opts, http.Filter(newRequestBodyLimits(conf.GetRequestBody()).filter), http.RequestDecoder(decodeRequest))
	opts = append(opts, spec.http.options...)
	opts = append(opts, http.Middleware(middlewares.build(spec.http.middlewares)...))
	return opts
}

// withoutPprof 对 /debug/pprof 路径返回 404，其余交给 next。
func withoutPprof(next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.URL.Path == "/debug/pprof" || strings.HasPrefix(r.URL.Path, "/debug/pprof/") {
			nethttp.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
