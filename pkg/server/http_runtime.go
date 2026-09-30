package server

import (
	"context"
	"errors"
	"net"
	"net/url"
	"sync"

	"github.com/go-kratos/kratos/v2/transport/http"
)

// httpRuntime 保留原生路由服务器，由 Foundation 持有其监听器和准备阶段。
type httpRuntime struct {
	HTTPServer
	listener *managedListener
	// prepareOnce 串行完成监听准备和 SDK Endpoint 初始化，Start 只读取已发布结果。
	prepareOnce sync.Once
	endpoint    *url.URL
	prepareErr  error
}

func newManagedHTTPServer(network, address string, supplied net.Listener, options ...http.ServerOption) *httpRuntime {
	if network == "" {
		network = "tcp"
	}
	if address == "" {
		address = ":0"
	}
	listener := newManagedListener(network, address, supplied)
	// 监听类原生 option 不再拥有最终覆盖权，SDK 始终借用 Foundation 的监听器。
	options = append(options, http.Network(network), http.Address(address), http.Listener(listener))
	return &httpRuntime{HTTPServer: http.NewServer(options...), listener: listener}
}

func (s *httpRuntime) prepare(ctx context.Context) error {
	s.prepareOnce.Do(func() {
		if s.prepareErr = s.listener.prepare(ctx); s.prepareErr != nil {
			return
		}
		s.endpoint, s.prepareErr = s.HTTPServer.Endpoint()
		if s.prepareErr != nil {
			// SDK 地址解析失败时尚未进入 Serve，必须由监听所有者立即回滚。
			s.prepareErr = errors.Join(s.prepareErr, s.listener.close(context.WithoutCancel(ctx)))
		}
	})
	return s.prepareErr
}

// Endpoint 准备监听并返回端点副本；构造期不执行绑定。
func (s *httpRuntime) Endpoint() (*url.URL, error) {
	if err := s.prepare(context.Background()); err != nil {
		return nil, err
	}
	endpoint := *s.endpoint
	return &endpoint, nil
}

// Start 把已准备的监听交给 Kratos Serve；任何启动返回路径都回收监听。
func (s *httpRuntime) Start(ctx context.Context) (err error) {
	// 调用方监听在构造时已转交所有权，准备阶段因取消退出时也需要释放。
	defer func() { err = errors.Join(err, s.listener.close(context.WithoutCancel(ctx))) }()
	if err = s.prepare(ctx); err != nil {
		// Stop 先于 Start 时没有业务故障，不重新打开已关闭的运行时。
		if err == net.ErrClosed {
			return nil
		}
		return err
	}
	return s.HTTPServer.Start(ctx)
}

// Stop 先保留 SDK 排空行为，再回收尚未进入 Serve 的监听。
func (s *httpRuntime) Stop(ctx context.Context) error {
	err := s.HTTPServer.Stop(ctx)
	return errors.Join(err, s.listener.close(ctx))
}

// AbortStartup 仅供应用在服务器尚未启动时回滚，不等待 stop_delay 或调用业务回调。
func (s *httpRuntime) AbortStartup(ctx context.Context) error {
	return s.listener.close(ctx)
}
