package server

import (
	"fmt"
	"net/url"

	"github.com/go-kratos/kratos/v2/transport/http"
	foundationhttp "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// HTTPServer 是 Foundation endpoint 使用的 Kratos HTTP Server。
type HTTPServer = *http.Server

// newHTTPServer 组装业务路由；监控端点由监听规划统一挂载。
func newHTTPServer(
	config componentConfig,
	opts httpServerOptions,
	spec *Spec,
	logger log.Logger,
	websockets *websocketHub,
) (HTTPServer, error) {
	if spec.httpDisabled(config.GetHttp().GetDisable()) {
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
			websocket.Handle(endpoint.path, endpoint.handler, endpoint.upgrader...)
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
