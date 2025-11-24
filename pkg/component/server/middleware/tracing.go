package middleware

import (
	"github.com/go-kratos/kratos/v2/middleware"
	tracing2 "github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/tracing"
)

func Tracing(tracing *tracing.Tracing, tracerName string) middleware.Middleware {
	var opts = []tracing2.Option{
		tracing2.WithTracerProvider(tracing.GetTracerProvider()),
	}
	if tracerName != "" {
		opts = append(opts, tracing2.WithTracerName(tracerName))
	}
	return tracing2.Server(opts...)
}
