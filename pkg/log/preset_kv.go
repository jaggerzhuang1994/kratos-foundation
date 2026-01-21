package log

import (
	"context"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
)

const defaultCallerDepth = 6

type PresetKv map[string]any

const tsKey = "ts"
const serviceIDKey = "service.id"
const serviceNameKey = "service.name"
const serviceVersionKey = "service.version"
const traceIDKey = "trace.id"
const spanIDKey = "span.id"
const callerKey = "caller"

var defaultPreset = []string{
	tsKey,
	serviceIDKey,
	serviceNameKey,
	serviceVersionKey,
	traceIDKey,
	spanIDKey,
	// callerKey,
}

func NewPresetKv(appInfo app_info.AppInfo) PresetKv {
	return PresetKv{
		tsKey: log.DefaultTimestamp,
		serviceIDKey: log.Valuer(func(context.Context) interface{} {
			return appInfo.GetId()
		}),
		serviceNameKey: log.Valuer(func(context.Context) interface{} {
			return appInfo.GetName()
		}),
		serviceVersionKey: log.Valuer(func(context.Context) interface{} {
			return appInfo.GetVersion()
		}),
		traceIDKey: tracing.TraceID(),
		spanIDKey:  tracing.SpanID(),
		callerKey:  log.Caller(defaultCallerDepth),
	}
}

var _ = bindValues

func bindValues(ctx context.Context, keyvals []any) {
	for i := 1; i < len(keyvals); i += 2 {
		if v, ok := keyvals[i].(log.Valuer); ok {
			keyvals[i] = v(ctx)
		}
	}
}

func containsValuer(keyvals []any) bool {
	for i := 1; i < len(keyvals); i += 2 {
		if _, ok := keyvals[i].(log.Valuer); ok {
			return true
		}
	}
	return false
}
