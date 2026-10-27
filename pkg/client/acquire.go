package client

import (
	"context"
	"fmt"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	stdgrpc "google.golang.org/grpc"
)

// AcquireHTTP 租用 HTTP/HTTPS 客户端，语义同 Factory.AcquireClient：成功后调用方必须在本次调用结束时执行 release。
// 连接配置为 gRPC 时立即释放租约并返回 ErrProtocolMismatch。
func AcquireHTTP(ctx context.Context, factory Factory, name string) (*kratoshttp.Client, func(), error) {
	httpClient, _, release, err := factory.AcquireClient(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	if httpClient == nil {
		release()
		return nil, nil, fmt.Errorf("%w: client %q is not HTTP", ErrProtocolMismatch, name)
	}
	return httpClient, release, nil
}

// AcquireGRPC 租用 gRPC 连接，语义同 Factory.AcquireClient；连接配置为 HTTP/HTTPS 时立即释放租约并返回 ErrProtocolMismatch。
func AcquireGRPC(ctx context.Context, factory Factory, name string) (*stdgrpc.ClientConn, func(), error) {
	_, conn, release, err := factory.AcquireClient(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	if conn == nil {
		release()
		return nil, nil, fmt.Errorf("%w: client %q is not gRPC", ErrProtocolMismatch, name)
	}
	return conn, release, nil
}
