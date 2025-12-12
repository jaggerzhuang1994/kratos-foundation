package tracing

import (
	"github.com/go-kratos/kratos/v2/middleware"
	tracing2 "github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
)

type Tracing = tracing.Tracing
type Config = kratos_foundation_pb.MiddlewareConfig_Tracing

func Enable(config *Config) bool {
	// 存在一个 disable 则禁用
	return !config.GetDisable()
}

func Server(tracing *tracing.Tracing, config *Config) middleware.Middleware {
	opts := newOpts(tracing, config)
	return tracing2.Server(opts...)
}

func Client(tracing *tracing.Tracing, config *Config) middleware.Middleware {
	opts := newOpts(tracing, config)
	return tracing2.Client(opts...)
}

func newOpts(tracing *tracing.Tracing, _ ...*Config) []tracing2.Option {
	var opts = []tracing2.Option{
		tracing2.WithTracerProvider(tracing.GetTracerProvider()),
	}

	opts = append(opts, tracing2.WithTracerName(tracing.GetDefaultTracerName()))

	return opts
}
