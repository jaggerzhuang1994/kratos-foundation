package client

import (
	"context"

	"github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/grpc"
)

type httpCallOptionsKey struct{}
type grpcCallOptionsKey struct{}

// WithHTTPCallOptions 为本次 HTTP 调用追加选项；不会修改父 Context 的选项切片。
func WithHTTPCallOptions(ctx context.Context, opts ...http.CallOption) context.Context {
	return context.WithValue(ctx, httpCallOptionsKey{}, append(HTTPCallOptionsFromContext(ctx), opts...))
}

// HTTPCallOptionsFromContext 返回独立的选项切片；选项内的引用仍由调用方管理。
func HTTPCallOptionsFromContext(ctx context.Context) []http.CallOption {
	opts, _ := ctx.Value(httpCallOptionsKey{}).([]http.CallOption)
	return append([]http.CallOption(nil), opts...)
}

// WithGRPCCallOptions 为本次 gRPC 调用追加选项；不会修改父 Context 的选项切片。
func WithGRPCCallOptions(ctx context.Context, opts ...grpc.CallOption) context.Context {
	return context.WithValue(ctx, grpcCallOptionsKey{}, append(GRPCCallOptionsFromContext(ctx), opts...))
}

// GRPCCallOptionsFromContext 返回独立的选项切片；Header 等输出参数不得跨并发调用共享。
func GRPCCallOptionsFromContext(ctx context.Context) []grpc.CallOption {
	opts, _ := ctx.Value(grpcCallOptionsKey{}).([]grpc.CallOption)
	return append([]grpc.CallOption(nil), opts...)
}
