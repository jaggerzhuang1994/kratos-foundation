package database

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/tracing"
	"gorm.io/gorm"
	tracing2 "gorm.io/plugin/opentelemetry/tracing"
)

type TracingPlugin gorm.Plugin

func NewTracingPlugin(
	config Config,
	tracing tracing.Tracing,
	serviceAttributes app_info.ServiceAttributes,
) TracingPlugin {
	conf := config.GetTracing()
	if conf.GetDisable() {
		return nil
	}

	var opts []tracing2.Option
	opts = append(opts, tracing2.WithTracerProvider(tracing.GetTracerProvider()))
	opts = append(opts, tracing2.WithAttributes(serviceAttributes...))

	if conf.GetExcludeQueryVars() {
		opts = append(opts, tracing2.WithoutQueryVariables())
	}

	if conf.GetExcludeMetrics() {
		opts = append(opts, tracing2.WithoutMetrics())
	}

	if conf.GetRecordStackTraceInSpan() {
		opts = append(opts, tracing2.WithRecordStackTrace())
	}

	if conf.GetExcludeServerAddress() {
		opts = append(opts, tracing2.WithoutServerAddress())
	}

	return tracing2.NewPlugin(opts...)
}
