package tracing

import (
	"github.com/go-kratos/kratos/v2/middleware"
	tracing2 "github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

// Config 是追踪中间件对应的 protobuf 配置类型。
type Config = *config_pb.Middleware_Tracing

const instrumentationName = "github.com/go-kratos/kratos/v2/middleware/tracing"

// Server 创建使用应用私有 TracerProvider 的服务端追踪中间件。
func Server(provider tracing.Provider, config Config) middleware.Middleware {
	if config.GetDisable() || provider.Disabled() {
		return nil
	}
	opts := newOpts(provider)
	return tracing2.Server(opts...)
}

// Client 创建使用应用私有 TracerProvider 的客户端追踪中间件。
func Client(provider tracing.Provider, config Config) middleware.Middleware {
	if config.GetDisable() || provider.Disabled() {
		return nil
	}
	opts := newOpts(provider)
	return tracing2.Client(opts...)
}

// newOpts 把基础追踪 Provider 显式转换为 Kratos 中间件选项。
func newOpts(provider tracing.Provider) []tracing2.Option {
	return []tracing2.Option{
		tracing2.WithTracerProvider(provider.TracerProvider()),
		tracing2.WithTracerName(instrumentationName),
	}
}
