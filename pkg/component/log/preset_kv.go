package log

import (
	"context"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
)

const TsKey = "ts"
const ServiceIDKey = "service.id"
const ServiceNameKey = "service.name"
const ServiceVersionKey = "service.version"
const TraceIDKey = "trace.id"
const SpanIDKey = "span.id"
const CallerKey = "caller"
const ModuleKey = "module"
const MsgKey = "msg"

var serviceID = log.Valuer(func(ctx context.Context) interface{} {
	app, ok := kratos.FromContext(ctx)
	if !ok {
		return ""
	}
	return app.ID()
})

var serviceName = log.Valuer(func(ctx context.Context) interface{} {
	app, ok := kratos.FromContext(ctx)
	if !ok {
		return ""
	}
	return app.Name()
})

var serviceVersion = log.Valuer(func(ctx context.Context) interface{} {
	app, ok := kratos.FromContext(ctx)
	if !ok {
		return ""
	}
	return app.Version()
})
