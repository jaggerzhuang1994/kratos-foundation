package server

import (
	"net/url"

	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/metric"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/server/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/tracing"
)

type HttpServerOptions []http.ServerOption

const httpServerLogModule = "server.http"

func NewHttpServer(
	_ bootstrap.Bootstrap,
	cfg *Config,
	log *log.Log,
	metrics *metric.Metrics,
	tracing *tracing.Tracing,
	hook *HookManager,
) *http.Server {
	if cfg.GetHttp().GetDisable() {
		return nil
	}
	log = log.WithModule(httpServerLogModule, cfg.GetLog())

	middlewares := middleware.NewServerMiddleware(log, metrics, tracing, cfg.GetMiddleware(), cfg.GetHttp().GetMiddleware())
	for _, httpServerMiddleware := range hook.httpServerMiddlewares {
		middlewares = httpServerMiddleware(middlewares)
	}

	opts := newHttpServerOptions(cfg, middlewares)
	for _, hookHttpServerOption := range hook.httpServerOptions {
		opts = hookHttpServerOption(opts)
	}

	srv := http.NewServer(opts...)
	return srv
}

func newHttpServerOptions(cfg *Config, middlewares middleware.Middlewares) HttpServerOptions {
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
	if len(middlewares) > 0 {
		opts = append(opts, http.Middleware(middlewares...))
	}
	return opts
}
