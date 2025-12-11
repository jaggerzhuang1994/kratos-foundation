package log

import (
	"context"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
)

const TsKey = "ts"
const ServiceIDKey = "service.id"
const ServiceNameKey = "service.name"
const ServiceVersionKey = "service.version"
const TraceIDKey = "trace.id"
const SpanIDKey = "span.id"
const CallerKey = "caller"

var defaultPreset = []string{
	TsKey,
	ServiceIDKey,
	ServiceNameKey,
	ServiceVersionKey,
	TraceIDKey,
	SpanIDKey,
	CallerKey,
}

const ModuleKey = "module"
const MsgKey = "msg"

var serviceID = log.Valuer(func(ctx context.Context) interface{} {
	ai, ok := app_info.FromContext(ctx)
	if !ok {
		return ""
	}
	return ai.GetId()
})

var serviceName = log.Valuer(func(ctx context.Context) interface{} {
	ai, ok := app_info.FromContext(ctx)
	if !ok {
		return ""
	}
	return ai.GetName()
})

var serviceVersion = log.Valuer(func(ctx context.Context) interface{} {
	ai, ok := app_info.FromContext(ctx)
	if !ok {
		return ""
	}
	return ai.GetVersion()
})

var traceID = tracing.TraceID()
var spanID = tracing.SpanID()
