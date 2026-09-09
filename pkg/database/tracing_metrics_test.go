package database

import (
	"context"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/proto"
)

type recordingGlobalMeterProvider struct {
	metric.MeterProvider
	calls int
}

func (p *recordingGlobalMeterProvider) Meter(name string, options ...metric.MeterOption) metric.Meter {
	p.calls++
	return p.MeterProvider.Meter(name, options...)
}

// Tracing 不得绕过 metrics.disable，也不得注册超出 Manager cleanup 管理范围的回调。
func TestManagerTracingKeepsMetricsWithExplicitProvider(t *testing.T) {
	for _, disabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "disabled", false: "enabled"}[disabled], func(t *testing.T) {
			reader := sdkmetric.NewManualReader()
			global := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			recording := &recordingGlobalMeterProvider{MeterProvider: global}
			previous := otel.GetMeterProvider()
			otel.SetMeterProvider(recording)
			t.Cleanup(func() {
				otel.SetMeterProvider(previous)
				if err := global.Shutdown(context.Background()); err != nil {
					t.Error(err)
				}
			})
			config := &config_pb.Database{
				Default: proto.String("primary"),
				Connections: map[string]*config_pb.DBConnection{
					"primary": {Driver: proto.String("sqlite3"), Dsn: ":memory:"},
				},
				Metrics: &config_pb.GormMetrics{Disable: proto.Bool(disabled)},
			}
			metrics := newManagerMetricsProvider()
			driver := new(recordingSQLiteDriver)
			manager, cleanup, err := newManagerWithDrivers(newManagerTestLogger(t), testconfig.New(t, "database", config),
				managerTestAppInfo{}, newManagerTracingProvider(false), metrics,
				map[string]DriverFactory{"sqlite3": driver.open})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			if err := manager.Connection(context.Background()).Exec("SELECT 1").Error; err != nil {
				t.Fatal(err)
			}
			if recording.calls != 0 {
				t.Errorf("tracing requested the global meter %d times", recording.calls)
			}
			var data metricdata.ResourceMetrics
			if err := reader.Collect(context.Background(), &data); err != nil {
				t.Fatal(err)
			}
			if len(data.ScopeMetrics) != 0 {
				t.Errorf("tracing registered global metrics in %d scopes", len(data.ScopeMetrics))
			}
			families, err := metrics.registry.Gather()
			if err != nil {
				t.Fatal(err)
			}
			if disabled && len(families) != 0 || !disabled && len(families) == 0 {
				t.Errorf("metrics disabled=%t, explicit registry families=%d", disabled, len(families))
			}
			cleanup()
			families, err = metrics.registry.Gather()
			if err != nil || len(families) != 0 {
				t.Errorf("metrics after cleanup: families=%d, error=%v", len(families), err)
			}
		})
	}
}
