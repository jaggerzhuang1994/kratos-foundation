package client

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/grpc"
)

type connNameKey struct{}
type timeoutKey struct{}
type httpCallOptionKey struct{}
type grpcCallOptionKey struct{}

// WithConnName 注入指定client连接
func WithConnName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, connNameKey{}, name)
}

// WithDefaultConnName 注入client连接默认值
func WithDefaultConnName(ctx context.Context, name string) context.Context {
	if _, ok := ctx.Value(connNameKey{}).(string); ok {
		return ctx
	}
	return WithConnName(ctx, name)
}

// ConnNameFromContext 提取连接名称
func ConnNameFromContext(ctx context.Context) string {
	conn, _ := ctx.Value(connNameKey{}).(string)
	return conn
}

// WithTimeout 注入超时时间
func WithTimeout(ctx context.Context, timeout time.Duration) context.Context {
	return context.WithValue(ctx, timeoutKey{}, timeout)
}

// WithDefaultTimeout 注入默认超时时间
func WithDefaultTimeout(ctx context.Context, timeout time.Duration) context.Context {
	if _, ok := ctx.Value(timeoutKey{}).(time.Duration); ok {
		return ctx
	}
	return WithTimeout(ctx, timeout)
}

// WithTimeoutContext 获取超时context, cancel
func WithTimeoutContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout, _ := ctx.Value(timeoutKey{}).(time.Duration)
	if timeout <= 0 {
		return ctx, func() {
		}
	}

	return context.WithTimeoutCause(ctx, timeout, ErrClientTimeout)
}

// WithHttpCallOption 注入额外http请求参数
func WithHttpCallOption(ctx context.Context, opts ...http.CallOption) context.Context {
	return context.WithValue(ctx, httpCallOptionKey{}, append(HttpCallOptionFromContext(ctx), opts...))
}

// HttpCallOptionFromContext 获取额外http请求参数
func HttpCallOptionFromContext(ctx context.Context) []http.CallOption {
	opts, _ := ctx.Value(httpCallOptionKey{}).([]http.CallOption)
	return opts
}

// WithGrpcCallOption 注入额外grpc请求参数
func WithGrpcCallOption(ctx context.Context, opts ...grpc.CallOption) context.Context {
	return context.WithValue(ctx, grpcCallOptionKey{}, append(GrpcCallOptionFromContext(ctx), opts...))
}

// GrpcCallOptionFromContext 获取额外grpc请求参数
func GrpcCallOptionFromContext(ctx context.Context) []grpc.CallOption {
	opts, _ := ctx.Value(grpcCallOptionKey{}).([]grpc.CallOption)
	return opts
}
