package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"net/netip"
	"strconv"
	"strings"
	"time"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// configureMonitoring 在构造期按地址归并监听，只创建 Server，不打开 socket。
// 运行时由 Bootstrap/App 统一启停；独立端口不继承业务路由、鉴权、TLS 或 Filter。
func configureMonitoring(config componentConfig, business HTTPServer, health *healthState, provider metrics.Provider) ([]*httpRuntime, []registeredEndpoint, error) {
	conf := config.GetHttp()
	mainAddr := conf.GetAddr()
	if business != nil && (conf.GetNetwork() == "tcp" || conf.GetNetwork() == "tcp4" || conf.GetNetwork() == "tcp6" || conf.GetNetwork() == "") {
		if mainAddr == "" {
			mainAddr = ":0"
		}
		var err error
		mainAddr, err = canonicalMonitoringAddr(mainAddr)
		if err != nil {
			return nil, nil, fmt.Errorf("server.http.addr: %w", err)
		}
	}
	// 空地址代表挂载在业务监听，即使业务使用 Unix socket 也可复用。
	destination := func(addr string) (string, bool, error) {
		if addr == "" {
			return "", business != nil, nil
		}
		normalized, err := canonicalMonitoringAddr(addr)
		if err != nil {
			return "", false, err
		}
		if normalized == mainAddr && business != nil {
			return "", true, nil
		}
		return normalized, true, nil
	}
	metricsConf := conf.GetMetrics()
	metricsAddr, metricsEnabled, err := destination(metricsConf.GetAddr())
	if err != nil && !metricsConf.GetDisable() {
		return nil, nil, fmt.Errorf("metrics addr: %w", err)
	}
	metricsEnabled = metricsEnabled && !metricsConf.GetDisable()
	healthAddr, healthEnabled, err := destination(health.config.Addr)
	if err != nil && !health.config.Disable {
		return nil, nil, fmt.Errorf("health addr: %w", err)
	}
	healthEnabled = healthEnabled && !health.config.Disable
	pprofConf := conf.GetPprof()
	pprofAddr := ""
	if pprofConf.GetEnable() {
		configured := pprofConf.GetAddr()
		if configured == "" {
			configured = metricsConf.GetAddr()
		}
		var mounted bool
		pprofAddr, mounted, err = destination(configured)
		if err != nil {
			return nil, nil, fmt.Errorf("pprof addr: %w", err)
		}
		// 剖析端点不能落到业务监听：未开启独立地址时宁可启动失败，也不对业务流量暴露。
		if !mounted || pprofAddr == "" {
			return nil, nil, errors.New("server.http.pprof requires a dedicated management address (pprof.addr or metrics.addr) distinct from server.http.addr")
		}
	}
	metricsPath := metricsConf.GetPath()
	if metricsEnabled {
		if strings.TrimSpace(metricsPath) == "" || metricsPath != strings.TrimSpace(metricsPath) || !strings.HasPrefix(metricsPath, "/") {
			return nil, nil, fmt.Errorf("http server metrics path %q must start with / and contain no surrounding whitespace", metricsPath)
		}
	}
	conflictPath := ""
	if metricsEnabled && healthEnabled && metricsAddr == healthAddr {
		conflictPath = metricsPath
	}
	if healthEnabled {
		if err := health.validate(conflictPath); err != nil {
			return nil, nil, err
		}
	}
	extras := make([]*httpRuntime, 0, 3)
	endpoints := make([]registeredEndpoint, 0, 6)
	byAddress := make(map[string]*httpRuntime)
	target := func(addr string) HTTPServer {
		if addr == "" {
			return business
		}
		if srv := byAddress[addr]; srv != nil {
			return srv.HTTPServer
		}
		srv := newManagedHTTPServer("tcp", addr, nil, kratoshttp.Timeout(0))
		// 独立管理端口只挂载明确的监控处理器，避免回退到全局 DefaultServeMux。
		srv.Handler = http.NotFoundHandler()
		srv.ReadHeaderTimeout = 5 * time.Second
		byAddress[addr] = srv
		extras = append(extras, srv)
		return srv.HTTPServer
	}
	if metricsEnabled {
		srv := target(metricsAddr)
		handler := promhttp.HandlerFor(provider.PrometheusGatherer(), promhttp.HandlerOpts{})
		next := srv.Handler
		// 使用精确路径，不将 path_prefix 或业务 Filter 带到监控端点。
		srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == metricsPath {
				handler.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
		endpoints = append(endpoints, registeredEndpoint{transport: "http", service: "metrics", method: http.MethodGet, path: metricsPath, listener: monitoringListener(metricsAddr)})
	}
	if pprofConf.GetEnable() {
		srv := target(pprofAddr)
		srv.Handler = pprofHandler(srv.Handler)
		endpoints = append(endpoints, registeredEndpoint{transport: "http", service: "pprof", method: http.MethodGet, path: pprofPrefix, listener: pprofAddr})
	}
	if healthEnabled {
		srv := target(healthAddr)
		srv.Handler = health.wrap(srv.Handler)
		for _, path := range []string{health.config.LivenessPath, health.config.ReadinessPath} {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				endpoints = append(endpoints, registeredEndpoint{transport: "http", service: "health", method: method, path: path, listener: monitoringListener(healthAddr)})
			}
		}
	}
	return extras, endpoints, nil
}

const pprofPrefix = "/debug/pprof/"

// pprofHandler 显式挂载 net/http/pprof 处理器，不依赖 DefaultServeMux 的注册副作用。
func pprofHandler(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(pprofPrefix, pprof.Index)
	mux.HandleFunc(pprofPrefix+"cmdline", pprof.Cmdline)
	mux.HandleFunc(pprofPrefix+"profile", pprof.Profile)
	mux.HandleFunc(pprofPrefix+"symbol", pprof.Symbol)
	mux.HandleFunc(pprofPrefix+"trace", pprof.Trace)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, pprofPrefix) {
			mux.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func monitoringListener(addr string) string {
	if addr == "" {
		return "business"
	}
	return addr
}

// canonicalMonitoringAddr 做确定性地址比较，不解析 DNS；系统仍负责检测绑定冲突。
func canonicalMonitoringAddr(addr string) (string, error) {
	if strings.TrimSpace(addr) != addr {
		return "", fmt.Errorf("invalid listen address %q", addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("listen address must be host:port: %w", err)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 0 || number > 65535 {
		return "", fmt.Errorf("invalid listen port %q", port)
	}
	if strings.ContainsAny(host, " /?#\t\r\n") {
		return "", fmt.Errorf("invalid listen host %q", host)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		host = ip.String()
	}
	if host == "0.0.0.0" {
		host = ""
	}
	return net.JoinHostPort(host, strconv.Itoa(number)), nil
}
