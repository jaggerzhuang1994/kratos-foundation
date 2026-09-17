package requestdebug

import (
	"context"
	"strings"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/request"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// clearOutgoingDebug 保留无关 metadata，并只在发现保留键时派生新 Context。
func clearOutgoingDebug(ctx context.Context) context.Context {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return ctx
	}
	changed := false
	for key := range md {
		if strings.EqualFold(key, Header) {
			delete(md, key)
			changed = true
		}
	}
	if changed {
		return metadata.NewOutgoingContext(ctx, md)
	}
	return ctx
}

// StreamClient 在创建流之前写入 metadata；普通 Kratos 消息中间件执行时已经太晚。
func StreamClient(config *config_pb.Middleware_RequestDebug) grpc.StreamClientInterceptor {
	propagate := config == nil || config.Propagate == nil || config.GetPropagate()
	return func(ctx context.Context, desc *grpc.StreamDesc, conn *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx = clearOutgoingDebug(ctx)
		if propagate && request.IsDebug(ctx) {
			ctx = metadata.AppendToOutgoingContext(ctx, Header, "1")
		}
		return streamer(ctx, desc, conn, method, opts...)
	}
}

type serverStream struct {
	// ServerStream 保留底层 gRPC 流的收发能力。
	grpc.ServerStream
	// ctx 保存应用调试策略后的流上下文，供整个流生命周期复用。
	ctx context.Context
}

func (s *serverStream) Context() context.Context { return s.ctx }

// StreamServer 在流入口应用当前 debug 中间件，状态固定到该流 Context。
// Kratos 外层拦截器已注入 Transport；不在 SendMsg/RecvMsg 时重复恢复标记。
func StreamServer(debug middleware.Middleware) grpc.StreamServerInterceptor {
	return func(service any, stream grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		_, err := debug(func(ctx context.Context, _ any) (any, error) {
			return nil, next(service, &serverStream{ServerStream: stream, ctx: ctx})
		})(stream.Context(), nil)
		return err
	}
}
