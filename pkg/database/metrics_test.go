package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/proto"
)

func TestMergeMetricLabelsValidatesNamesReservationsAndIdentityConflicts(t *testing.T) {
	labels, err := mergeMetricLabels(
		map[string]string{"region": "east"},
		[]attribute.KeyValue{
			attribute.String("service.name", "orders"),
			attribute.String("service.instance.id", "instance-1"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{
		"region":              "east",
		"service_name":        "orders",
		"service_instance_id": "instance-1",
	}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("merged labels = %#v, want %#v", labels, want)
	}

	for _, test := range []struct {
		name   string
		labels map[string]string
		attrs  []attribute.KeyValue
		want   string
	}{
		{name: "invalid name", labels: map[string]string{"bad-name": "value"}, want: "invalid"},
		{name: "reserved operation", labels: map[string]string{"operation": "manual"}, want: "reserved"},
		{name: "reserved result", labels: map[string]string{"result": "manual"}, want: "reserved"},
		{name: "reserved db name", labels: map[string]string{"db_name": "manual"}, want: "reserved"},
		{
			name:   "identity conflict",
			labels: map[string]string{"service_name": "manual"},
			attrs:  []attribute.KeyValue{attribute.String("service.name", "orders")},
			want:   "conflicts",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			labels, err := mergeMetricLabels(test.labels, test.attrs)
			if labels != nil || err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("mergeMetricLabels() = (%#v, %v), want %q error", labels, err, test.want)
			}
		})
	}
}

func TestNewMetricsCollectorRegistersAndUnregistersDatabaseStats(t *testing.T) {
	db, _ := newNoStatusQueryDB(t)
	factory := &connectionFactory{connections: map[*sql.DB]connectionPool{
		db: {db: db, name: "primary", driver: "mysql"},
	}}
	provider := newManagerMetricsProvider()
	config := &config_pb.Database{
		Default: proto.String("primary"),
		Metrics: &config_pb.GormMetrics{Labels: map[string]string{"region": "test"}},
	}

	collector, err := newMetricsCollector(
		factory,
		config,
		managerTestAppInfo{},
		provider,
	)
	if err != nil {
		t.Fatal(err)
	}
	if collector == nil || len(collector.dbStats) != 1 {
		t.Fatalf("database stats collector = %#v", collector)
	}
	if families, err := provider.registry.Gather(); err != nil || len(families) == 0 {
		t.Fatalf("registered database stats = %d families, %v", len(families), err)
	}
	collector.close()
	collector.close()
	if families, err := provider.registry.Gather(); err != nil || len(families) != 0 {
		t.Fatalf("database stats after close = %d families, %v", len(families), err)
	}

	disabled := true
	if collector, err := newMetricsCollector(
		factory,
		&config_pb.Database{Metrics: &config_pb.GormMetrics{Disable: &disabled}},
		managerTestAppInfo{},
		provider,
	); err != nil || collector != nil {
		t.Fatalf("disabled metrics collector = (%v, %v)", collector, err)
	}
	if collector, err := newMetricsCollector(
		factory,
		config,
		managerTestAppInfo{},
		nilDatabaseMetricsProvider{},
	); collector != nil || err == nil || !strings.Contains(err.Error(), "registerer is nil") {
		t.Fatalf("nil registry metrics collector = (%v, %v)", collector, err)
	}
}

func TestNewMetricsCollectorDoesNotQueryMySQLStatus(t *testing.T) {
	db, queries := newNoStatusQueryDB(t)
	factory := &connectionFactory{connections: map[*sql.DB]connectionPool{
		db: {db: db, name: "primary", driver: "mysql"},
	}}
	provider := newManagerMetricsProvider()
	collector, err := newMetricsCollector(
		factory,
		&config_pb.Database{Default: proto.String("primary")},
		managerTestAppInfo{},
		provider,
	)
	if err != nil {
		t.Fatal(err)
	}
	if collector == nil {
		t.Fatal("database metrics collector is nil")
	}
	t.Cleanup(collector.close)
	select {
	case query := <-queries:
		t.Fatalf("application metrics executed MySQL status query %q", query)
	case <-time.After(20 * time.Millisecond):
	}
}

var noStatusDriverID atomic.Uint64

func newNoStatusQueryDB(t testing.TB) (*sql.DB, <-chan string) {
	t.Helper()
	queries := make(chan string, 1)
	name := fmt.Sprintf("database_no_status_%d", noStatusDriverID.Add(1))
	sql.Register(name, noStatusDriver{queries: queries})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, queries
}

type noStatusDriver struct{ queries chan<- string }

func (d noStatusDriver) Open(string) (driver.Conn, error) {
	return noStatusConn(d), nil
}

type noStatusConn struct{ queries chan<- string }

func (noStatusConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare unsupported")
}
func (noStatusConn) Close() error              { return nil }
func (noStatusConn) Begin() (driver.Tx, error) { return nil, errors.New("transaction unsupported") }
func (c noStatusConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.queries <- query
	return nil, errors.New("query unsupported")
}

var _ driver.QueryerContext = noStatusConn{}

type nilDatabaseMetricsProvider struct{}

func (nilDatabaseMetricsProvider) Meter(
	name string,
	options ...metric.MeterOption,
) metric.Meter {
	return metricnoop.NewMeterProvider().Meter(name, options...)
}

func (nilDatabaseMetricsProvider) MeterProvider() metric.MeterProvider {
	return metricnoop.NewMeterProvider()
}

func (nilDatabaseMetricsProvider) PrometheusGatherer() prometheus.Gatherer { return nil }

func (nilDatabaseMetricsProvider) PrometheusRegisterer() prometheus.Registerer { return nil }

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
				Tracing: &config_pb.GormTracing{Disable: proto.Bool(false)},
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
