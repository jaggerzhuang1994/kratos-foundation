package server

import (
	"net/url"

	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/transport"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewHttpServer 默认 http 服务器
func NewHttpServer(
	cfg *Config,
	register *Register,
	hook *Hook,
) *http.Server {
	if cfg.GetHttp().GetDisable() {
		return nil
	}

	opts := newHttpServerOptions(cfg)
	opts = append(opts, http.Middleware(hook.middleware...))

	srv := http.NewServer(opts...)
	// prometheus上报路由
	if !cfg.GetHttp().GetMetrics().GetDisable() {
		srv.Handle(cfg.GetHttp().GetMetrics().GetPath(), promhttp.Handler())
	}
	// hook回调
	for _, fn := range hook.httpServer {
		fn(srv)
	}
	register.RegisterServer(srv)
	return srv
}

func newHttpServerOptions(cfg *Config) []http.ServerOption {
	httpCfg := cfg.GetHttp()
	var opts []http.ServerOption
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
	//// 设置http接口的超时时间
	//if httpCfg.GetTimeout() != nil {
	//	opts = append(opts, http.Timeout(httpCfg.GetTimeout().AsDuration()))
	//}
	// 使用中间件来控制超时 需要显式设置为 0，否则内部会有默认值1s
	opts = append(opts, http.Timeout(0))

	if httpCfg.GetDisableStrictSlash() {
		opts = append(opts, http.StrictSlash(false))
	}
	if httpCfg.GetPathPrefix() != "" {
		opts = append(opts, http.PathPrefix(httpCfg.GetPathPrefix()))
	}
	// http返回错误信息处理
	opts = append(opts, http.ErrorEncoder(transport.HttpErrorEncoder()))
	return opts
}
