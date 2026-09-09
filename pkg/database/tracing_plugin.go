package database

import (
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/otelattr"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	foundationtracing "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"gorm.io/gorm"
	gormtracing "gorm.io/plugin/opentelemetry/tracing"
)

// newTracingPlugin creates the configured GORM OpenTelemetry plugin, or nil when disabled.
func newTracingPlugin(
	config *config_pb.Database,
	appInfo appinfo.AppInfo,
	provider foundationtracing.Provider,
) gorm.Plugin {
	tracingConfig := config.GetTracing()
	if tracingConfig.GetDisable() || provider.Disabled() {
		return nil
	}

	options := []gormtracing.Option{
		gormtracing.WithTracerProvider(provider.TracerProvider()),
		gormtracing.WithAttributes(otelattr.ServiceAttributes(appInfo)...),
		// 指标由 Manager 的显式 Provider 与 cleanup 管理，禁止插件注册全局回调。
		gormtracing.WithoutMetrics(),
	}
	if tracingConfig.GetExcludeQueryVars() {
		options = append(options, gormtracing.WithoutQueryVariables())
	}
	if tracingConfig.GetRecordStackTraceInSpan() {
		options = append(options, gormtracing.WithRecordStackTrace())
	}
	if tracingConfig.GetExcludeServerAddress() {
		options = append(options, gormtracing.WithoutServerAddress())
	}
	return gormtracing.NewPlugin(options...)
}
