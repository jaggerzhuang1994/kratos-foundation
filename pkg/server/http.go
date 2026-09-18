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

// HTTPHandler 处理一条自定义 HTTP 路由；Request Context 包含服务端中间件派生状态。
type HTTPHandler func(nethttp.ResponseWriter, *nethttp.Request) error

// HandleHTTP 创建一条自动执行服务端中间件链的自定义 HTTP 路由注册回调。
func HandleHTTP(method, path string, handler HTTPHandler) HTTPEndpoint {
	if handler == nil {
		return nil
	}
	return func(server HTTPServer) error {
		server.Route("/").Handle(method, path, middlewareHTTPHandler(path, handler))
		return nil
	}
}

// middlewareHTTPHandler 让声明式自定义路由复用生成式 HTTP 接口的中间件执行边界。
func middlewareHTTPHandler(operation string, handler HTTPHandler) http.HandlerFunc {
	return func(ctx http.Context) error {
		http.SetOperation(ctx, operation)
		next := ctx.Middleware(func(callCtx context.Context, req any) (any, error) {
			request, ok := req.(*nethttp.Request)
			if !ok || request == nil {
				return nil, fmt.Errorf("http middleware request has type %T, want *http.Request", req)
			}
			// 自定义处理器应观察中间件替换后的请求及其派生 Context。
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
	for _, endpoint := range spec.http.endpoints {
		if err := endpoint(srv); err != nil {
			return nil, fmt.Errorf("register HTTP endpoint: %w", err)
		}
	}

	if len(spec.http.websockets) > 0 {
		if websockets == nil {
			return nil, fmt.Errorf("websocket hub is nil")
		}
		websocket := newWebSocketServer(logger, srv, websockets)
		for _, endpoint := range spec.http.websockets {
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
