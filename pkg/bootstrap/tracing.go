package bootstrap

import (
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// TracingBootstrap 标记 tracing 的组装贡献已完成。
type TracingBootstrap struct{}

// NewTracingBootstrap 将已有组件接入应用组装，不启动运行时。
func NewTracingBootstrap() (TracingBootstrap, error) {
	log.RegisterFields(
		log.TraceIDKey, tracing.TraceID(),
		log.SpanIDKey, tracing.SpanID(),
	)
	return TracingBootstrap{}, nil
}
