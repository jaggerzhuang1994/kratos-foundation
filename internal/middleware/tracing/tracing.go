package tracing

import (
	"github.com/go-kratos/kratos/v2/middleware"
	tracing2 "github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
)

type Tracing = tracing.Tracing
type Config = *config_pb.Middleware_Tracing

func Server(tracing tracing.Tracing, config Config) middleware.Middleware {
	if config.GetDisable() {
		return nil
	}
	opts := newOpts(tracing)
	return tracing2.Server(opts...)
}

func Client(tracing tracing.Tracing, config Config) middleware.Middleware {
	if config.GetDisable() {
		return nil
	}
	opts := newOpts(tracing)
	return tracing2.Client(opts...)
}

func newOpts(tracing tracing.Tracing) []tracing2.Option {
	var opts = []tracing2.Option{
		tracing2.WithTracerProvider(tracing.GetTracerProvider()),
	}

	opts = append(opts, tracing2.WithTracerName(tracing.GetTracerName()))

	return opts
}
