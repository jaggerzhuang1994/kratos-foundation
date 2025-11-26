package server

import (
	"net/url"

	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/metric"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/server/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/transport"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type HttpServerOptions []http.ServerOption

const httpServerLogModule = "server.http"

func NewHttpServer(
	cfg *Config,
	log *log.Log,
	metrics *metric.Metrics,
	tracing *tracing.Tracing,
	hook *HookManager,
	routeTimeoutMiddleware RouteTimeoutMiddleware,
) *http.Server {
	if cfg.GetHttp().GetDisable() {
		return nil
	}
	log = log.WithModule(httpServerLogModule, cfg.GetLog())

	middlewares := middleware.NewServerMiddleware(log, metrics, tracing, cfg.GetMiddleware(), cfg.GetHttp().GetMiddleware())
	for _, httpServerMiddleware := range hook.httpServerMiddlewares {
		middlewares = httpServerMiddleware(middlewares)
	}

	opts := newHttpServerOptions(cfg)
	for _, hookHttpServerOption := range hook.httpServerOptions {
		opts = hookHttpServerOption(opts)
	}

	// middleware放在最后，不能被 httpServerOptions 覆盖，需要覆盖 middleware 使用 httpServerMiddlewares
	// 指定路由超时设计，使用定制中间件来实现
	if routeTimeoutMiddleware != nil {
		middlewares = append(middlewares, (middleware.Middleware)(routeTimeoutMiddleware))
	}
	opts = append(opts, http.Middleware(middlewares...))

	// http返回错误信息处理
	opts = append(opts, http.ErrorEncoder(transport.HttpErrorEncoder()))
	srv := http.NewServer(opts...)

	if !cfg.GetHttp().GetMetrics().GetDisable() {
		srv.Handle(cfg.GetHttp().GetMetrics().GetPath(), promhttp.Handler()) // prometheus上报路由
	}
	return srv
}

func newHttpServerOptions(cfg *Config) HttpServerOptions {
	httpCfg := cfg.GetHttp()
	var opts HttpServerOptions
	// 监听（"tcp", "tcp4", "tcp6", "unix" or "unixpacket"）
	if httpCfg.GetNetwork() != "" {
		opts = append(opts, http.Network(httpCfg.GetNetwork()))
	}
	// 监听的host:port
	if httpCfg.GetAddr() != "" {
		opts = append(opts, http.Address(httpCfg.GetAddr()))
	}
	// 设置http对外暴露的端点
	if httpCfg.GetEndpoint() != nil {
		opts = append(opts, http.Endpoint(&url.URL{Scheme: httpCfg.GetEndpoint().GetScheme(), Host: httpCfg.GetEndpoint().GetHost()}))
	}
	// 设置http接口的超时时间
	if httpCfg.GetTimeout() != nil {
		opts = append(opts, http.Timeout(httpCfg.GetTimeout().AsDuration()))
	}
	if httpCfg.GetDisableStrictSlash() {
		opts = append(opts, http.StrictSlash(false))
	}
	if httpCfg.GetPathPrefix() != "" {
		opts = append(opts, http.PathPrefix(httpCfg.GetPathPrefix()))
	}
	return opts
}
