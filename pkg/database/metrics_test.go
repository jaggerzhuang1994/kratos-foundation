package database

import (
	"database/sql"
	"database/sql/driver"
	"maps"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
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
	db, _ := newMySQLMetricsScriptDB(t)
	factory := &connectionFactory{connections: map[*sql.DB]connectionPool{
		db: {db: db, name: "primary", driver: "sqlite3"},
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
		newManagerTestLogger(t),
		provider,
	)
	if err != nil {
		t.Fatal(err)
	}
	if collector == nil || len(collector.dbStats) != 1 || collector.mysql != nil {
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
		newManagerTestLogger(t),
		provider,
	); err != nil || collector != nil {
		t.Fatalf("disabled metrics collector = (%v, %v)", collector, err)
	}
	if collector, err := newMetricsCollector(
		factory,
		config,
		managerTestAppInfo{},
		newManagerTestLogger(t),
		nilDatabaseMetricsProvider{},
	); collector != nil || err == nil || !strings.Contains(err.Error(), "registerer is nil") {
		t.Fatalf("nil registry metrics collector = (%v, %v)", collector, err)
	}
}

func TestNewMetricsCollectorIntegratesMySQLSnapshotAndDatabaseLabel(t *testing.T) {
	db, script := newMySQLMetricsScriptDB(t, mysqlMetricsQueryStep{
		rows: [][]driver.Value{{"Uptime", "30"}},
	})
	factory := &connectionFactory{connections: map[*sql.DB]connectionPool{
		db: {db: db, name: "primary", driver: "mysql"},
	}}
	provider := newManagerMetricsProvider()
	config := &config_pb.Database{
		Default: proto.String("primary"),
		Metrics: &config_pb.GormMetrics{
			RefreshInterval: durationpb.New(time.Hour),
			Mysql: &config_pb.GormMetrics_Mysql{
				Prefix:        proto.String("mysql_"),
				VariableNames: []string{"Uptime"},
			},
		},
	}
	collector, err := newMetricsCollector(
		factory,
		config,
		managerTestAppInfo{},
		newManagerTestLogger(t),
		provider,
	)
	if err != nil {
		t.Fatal(err)
	}
	if collector == nil || collector.mysql == nil {
		t.Fatalf("MySQL metrics collector = %#v", collector)
	}
	t.Cleanup(collector.close)
	if query := receiveMySQLMetricsQuery(t, script.calls); query != "SHOW STATUS" {
		t.Fatalf("initial MySQL metrics query = %q", query)
	}
	waitForMySQLMetricsValues(t, collector.mysql, map[string]float64{"Uptime": 30})

	families, err := provider.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, family := range families {
		if family.GetName() != "mysql_Uptime" {
			continue
		}
		found = true
		if len(family.Metric) != 1 || family.Metric[0].Gauge.GetValue() != 30 {
			t.Fatalf("MySQL metric family = %#v", family)
		}
		labels := make(map[string]string, len(family.Metric[0].Label))
		for _, label := range family.Metric[0].Label {
			labels[label.GetName()] = label.GetValue()
		}
		if labels["db_name"] != "primary" || labels["service_name"] != "database-test" {
			t.Fatalf("MySQL metric labels = %#v", labels)
		}
	}
	if !found {
		t.Fatalf("mysql_Uptime not found in %d metric families", len(families))
	}
}

func waitForMySQLMetricsValues(
	t testing.TB,
	collector *mysqlMetricsCollector,
	want map[string]float64,
) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		collector.mu.RLock()
		got := make(map[string]float64, len(collector.values))
		maps.Copy(got, collector.values)
		collector.mu.RUnlock()
		if reflect.DeepEqual(got, want) {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("timed out waiting for MySQL metrics values: got %#v want %#v", got, want)
		}
	}
}

var _ prometheus.Collector = (*mysqlStatusMetric)(nil)

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
