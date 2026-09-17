package tracing

import (
	"github.com/go-kratos/kratos/v2/middleware"
	tracing2 "github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Config 是追踪中间件对应的 protobuf 配置类型。
type Config = *config_pb.Middleware_Tracing

const instrumentationName = "github.com/go-kratos/kratos/v2/middleware/tracing"

// correlationProvider 只生成和传播请求关联 ID，不记录 Span，也没有 Processor 或 Exporter。
var correlationProvider oteltrace.TracerProvider = tracesdk.NewTracerProvider(
	tracesdk.WithSampler(tracesdk.NeverSample()),
)

// Server 创建使用应用私有 TracerProvider 的服务端追踪中间件。
func Server(provider tracing.Provider, config Config) middleware.Middleware {
	opts := newOpts(correlationTracerProvider(provider, config))
	return tracing2.Server(opts...)
}

// Client 创建使用应用私有 TracerProvider 的客户端追踪中间件。
func Client(provider tracing.Provider, config Config) middleware.Middleware {
	opts := newOpts(correlationTracerProvider(provider, config))
	return tracing2.Client(opts...)
}

// newOpts 把基础追踪 Provider 显式转换为 Kratos 中间件选项。
func newOpts(provider oteltrace.TracerProvider) []tracing2.Option {
	return []tracing2.Option{
		tracing2.WithTracerProvider(provider),
		tracing2.WithTracerName(instrumentationName),
	}
}

// correlationTracerProvider 在任一 tracing 开关关闭时隔离真实 Provider，确保只关联不采样。
func correlationTracerProvider(provider tracing.Provider, config Config) oteltrace.TracerProvider {
	if config.GetDisable() || provider.Disabled() {
		return correlationProvider
	}
	return provider.TracerProvider()
}
