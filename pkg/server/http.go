package server

import (
	"net/url"

	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/transport"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type HttpServer = *http.Server

// NewHttpServer 默认 http 服务器
func NewHttpServer(
	config Config,
	opts HttpServerOptions,
) HttpServer {
	if config.GetHttp().GetDisable() {
		return nil
	}
	srv := http.NewServer(opts...)
	// prometheus 上报路由
	if !config.GetHttp().GetMetrics().GetDisable() {
		srv.Handle(config.GetHttp().GetMetrics().GetPath(), promhttp.Handler())
	}
	return srv
}

type HttpServerOptions []http.ServerOption

func NewHttpServerOptions(config Config, middleware Middlewares) (opts HttpServerOptions) {
	conf := config.GetHttp()
	// 监听（"tcp", "tcp4", "tcp6", "unix" or "unixpacket"）
	if conf.GetNetwork() != "" {
		opts = append(opts, http.Network(conf.GetNetwork()))
	}
	// 监听的 host:port
	if conf.GetAddr() != "" {
		opts = append(opts, http.Address(conf.GetAddr()))
	}
	// 设置 http 对外暴露的端点
	if conf.GetEndpoint() != nil {
		opts = append(opts, http.Endpoint(&url.URL{Scheme: conf.GetEndpoint().GetScheme(), Host: conf.GetEndpoint().GetHost()}))
	}
	// 使用中间件来控制超时 需要显式设置为 0，否则内部会有默认值1s
	opts = append(opts, http.Timeout(0))

	if conf.GetDisableStrictSlash() {
		opts = append(opts, http.StrictSlash(false))
	}
	if conf.GetPathPrefix() != "" {
		opts = append(opts, http.PathPrefix(conf.GetPathPrefix()))
	}
	// http 返回错误信息处理
	opts = append(opts, http.ErrorEncoder(transport.HttpErrorEncoder()))
	// 中间件
	opts = append(opts, http.Middleware(middleware...))
	return opts
}
