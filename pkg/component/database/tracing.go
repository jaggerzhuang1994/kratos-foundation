package database

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/tracing"
	"gorm.io/gorm"
	tracing2 "gorm.io/plugin/opentelemetry/tracing"
)

type TracingPlugin gorm.Plugin

func NewTracingPlugin(
	c *Config,
	tracing *tracing.Tracing,
) TracingPlugin {
	cfg := c.GetTracing()
	if cfg.GetDisable() {
		return nil
	}

	var opts []tracing2.Option
	opts = append(opts, tracing2.WithTracerProvider(tracing.GetTracerProvider()))

	if cfg.GetExcludeQueryVars() {
		opts = append(opts, tracing2.WithoutQueryVariables())
	}

	if cfg.GetExcludeMetrics() {
		opts = append(opts, tracing2.WithoutMetrics())
	}

	if cfg.GetRecordStackTraceInSpan() {
		opts = append(opts, tracing2.WithRecordStackTrace())
	}

	if cfg.GetExcludeServerAddress() {
		opts = append(opts, tracing2.WithoutServerAddress())
	}

	return tracing2.NewPlugin(opts...)
}
