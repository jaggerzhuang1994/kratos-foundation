package log

import (
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

// Valuer 根据日志写入上下文计算字段值。
type Valuer = kratoslog.Valuer

const (
	moduleKey          = "module"
	defaultMsgKey      = "msg"
	defaultCallerDepth = 6

	TsKey             = "ts"
	CallerKey         = "caller"
	ServiceIDKey      = "service.id"
	ServiceNameKey    = "service.name"
	ServiceVersionKey = "service.version"
	TraceIDKey        = "trace.id" // 追踪 ID
	SpanIDKey         = "span.id"  // 跨度 ID
)

func newPreset(timeFormat string, callerDepth int) []any {
	if timeFormat == "" {
		timeFormat = time.RFC3339
	}
	if callerDepth == 0 {
		callerDepth = defaultCallerDepth
	}
	return []any{
		TsKey, kratoslog.Timestamp(timeFormat),
		CallerKey, kratoslog.Caller(callerDepth),
	}
}
