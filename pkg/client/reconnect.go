package client

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/reconnect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
)

func grpcReconnectOption() grpc.DialOption {
	policy := backoff.DefaultConfig
	// 保留 gRPC 连接状态机及抖动，缩短长时间断网后的最大等待基准。
	policy.MaxDelay = 5 * time.Second
	return grpc.WithConnectParams(grpc.ConnectParams{
		Backoff: policy, MinConnectTimeout: 5 * time.Second,
	})
}

func newHTTPClientTransport(ctx context.Context, secure bool) (http.RoundTripper, *tls.Config, func()) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		// 自定义 RoundTripper 拥有自己的连接管理，不在外层重放请求。
		return http.DefaultTransport, tlsConfig, nil
	}
	transport = transport.Clone()
	// 零值原本只保留每主机 2 条空闲连接，32 并发波次会反复建连。
	// 仅调整未显式设置的每主机空闲容量；全局空闲预算及活动连接限制沿用调用方设置。
	if transport.MaxIdleConnsPerHost == 0 {
		transport.MaxIdleConnsPerHost = 32
	}
	if secure {
		if transport.TLSClientConfig != nil {
			tlsConfig = transport.TLSClientConfig.Clone()
			tlsConfig.MinVersion = max(tlsConfig.MinVersion, tls.VersionTLS12)
		}
		transport.TLSClientConfig = tlsConfig
	}
	dial := transport.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	lifetime, closeDialer := context.WithCancel(ctx)
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		// net/http 可能将拨号移出请求取消链；显式绑定资源生命期，使 cleanup 能终止等待。
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		stop := context.AfterFunc(lifetime, cancel)
		defer stop()
		return dialHTTPConnection(ctx, dial, network, address)
	}
	return transport, tlsConfig, func() {
		closeDialer()
		transport.CloseIdleConnections()
	}
}

func dialHTTPConnection(
	ctx context.Context,
	dial func(context.Context, string, string) (net.Conn, error),
	network, address string,
) (net.Conn, error) {
	var policy reconnect.Backoff
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		conn, err := dial(ctx, network, address)
		if err == nil || attempt == 4 || !reconnect.Transient(err) {
			return conn, err
		}
		// 此时尚未发送 HTTP 请求；已写出请求后的网络错误仍交给调用方处理。
		if err := policy.Wait(ctx, attempt); err != nil {
			return nil, err
		}
	}
}
