package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"gorm.io/gorm"
)

func TestSQLMetricsManagerOperations(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "enabled", true: "disabled"}[disabled], func(t *testing.T) {
			provider := newManagerMetricsProvider()
			conf := &config_pb.Database{Default: proto.String("primary"), Connections: map[string]*config_pb.DBConnection{"primary": {Driver: proto.String("sqlite3"), Dsn: ":memory:"}}, Metrics: &config_pb.GormMetrics{Disable: proto.Bool(disabled)}}
			driver := new(recordingSQLiteDriver)
			mgr, cleanup, err := newManagerWithDrivers(newManagerTestLogger(t), testconfig.New(t, "database", conf), managerTestAppInfo{}, newManagerTracingProvider(true), provider, map[string]DriverFactory{"sqlite3": driver.open})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			db := mgr.Connection(context.Background())
			if err := db.Exec("CREATE TABLE manager_records (id integer primary key, name text)").Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&managerRecord{ID: 1, Name: "private-value"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&managerRecord{ID: 1, Name: "duplicate"}).Error; err == nil {
				t.Fatal("expected duplicate failure")
			}
			if err := db.First(&managerRecord{}, 99).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("missing row: %v", err)
			}
			if err := db.Session(&gorm.Session{DryRun: true}).Create(&managerRecord{ID: 2}).Error; err != nil {
				t.Fatal(err)
			}

			if err := db.First(&managerRecord{}, 1).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&managerRecord{}).Where("id = ?", 1).Update("name", "updated").Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Delete(&managerRecord{}, 1).Error; err != nil {
				t.Fatal(err)
			}
			var value int
			if err := db.Raw("SELECT 1").Row().Scan(&value); err != nil || value != 1 {
				t.Fatalf("row=%d, err=%v", value, err)
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if err := db.WithContext(canceled).Exec("SELECT 1").Error; !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled exec: %v", err)
			}
			// 原生 SQL 绕过 GORM 回调，不增加任何操作时间序列。
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sqlDB.ExecContext(context.Background(), "SELECT 1"); err != nil {
				t.Fatal(err)
			}

			families, err := provider.registry.Gather()
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]float64{}
			histograms := 0
			for _, family := range families {
				if disabled && (family.GetName() == "database_sql_slow_operations_total" || family.GetName() == "database_sql_slow_threshold_seconds") {
					t.Errorf("disabled slow metric: %s", family.GetName())
				}
				if family.GetName() != "database_sql_operations_total" && family.GetName() != "database_sql_operation_duration_seconds" {
					continue
				}
				for _, metric := range family.Metric {
					labels := map[string]string{}
					for _, label := range metric.Label {
						labels[label.GetName()] = label.GetValue()
					}
					if labels["db_name"] != "primary" {
						t.Errorf("db_name: %v", labels)
					}
					for key := range labels {
						switch key {
						case "db_name", "operation", "result", "service_name", "service_version", "service_instance_id":
						default:
							t.Errorf("unexpected label %q", key)
						}
					}
					key := labels["operation"] + "/" + labels["result"]
					if family.GetName() == "database_sql_operations_total" {
						got[key] = metric.GetCounter().GetValue()
					} else {
						histograms++
						if metric.GetHistogram().GetSampleCount() != 1 || metric.GetHistogram().GetSampleSum() < 0 {
							t.Errorf("histogram %s: %v", key, metric)
						}
					}
				}
			}
			if histograms != len(got) {
				t.Errorf("histogram series=%d, counters=%d", histograms, len(got))
			}
			if disabled {
				if len(got) != 0 {
					t.Errorf("disabled observations: %v", got)
				}
			} else {
				for _, key := range []string{"raw/success", "raw/error", "create/success", "create/error", "query/not_found", "query/success", "update/success", "delete/success", "row/success"} {
					if got[key] != 1 {
						t.Errorf("%s count=%v, want 1; all=%v", key, got[key], got)
					}
				}
				if len(got) != 9 {
					t.Errorf("unexpected series: %v", got)
				}
			}
			cleanup()
			families, err = provider.registry.Gather()
			if err != nil || len(families) != 0 {
				t.Fatalf("cleanup: %d families, %v", len(families), err)
			}
		})
	}
}

// 任一注册失败须回滚此前成功项，不能注销其他组件已经拥有的指标。
func TestSQLMetricsRegistrationRollback(t *testing.T) {
	for _, name := range []string{"database_sql_operations_total", "database_sql_operation_duration_seconds", "database_sql_slow_operations_total", "database_sql_slow_threshold_seconds"} {
		t.Run(name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			owned := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: "Owned by another component."})
			registry.MustRegister(owned)
			if _, err := newSQLMetrics(registry); err == nil {
				t.Fatal("expected registration conflict")
			}
			families, err := registry.Gather()
			if err != nil || len(families) != 1 || families[0].GetName() != name {
				t.Fatalf("rollback families=%v, err=%v", families, err)
			}
			// 空向量不会出现在 Gather；重新注册其他描述符才能发现泄漏。
			fresh, err := newSQLMetrics(prometheus.NewRegistry())
			if err != nil {
				t.Fatal(err)
			}
			for metricName, collector := range map[string]prometheus.Collector{
				"database_sql_operations_total":           fresh.operations,
				"database_sql_operation_duration_seconds": fresh.duration,
				"database_sql_slow_operations_total":      fresh.slow,
				"database_sql_slow_threshold_seconds":     fresh.threshold,
			} {
				if metricName != name {
					if err := registry.Register(collector); err != nil {
						t.Errorf("leaked %s: %v", metricName, err)
					}
				}
			}
		})
	}
}

func TestSQLMetricsSlowThresholdBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		threshold time.Duration
		elapsed   time.Duration
		err       error
		result    string
		wantSlow  float64
	}{
		{"below", time.Second, time.Second - time.Nanosecond, nil, "success", 0},
		{"equal", time.Second, time.Second, nil, "success", 0},
		{"above", time.Second, time.Second + time.Nanosecond, nil, "success", 1},
		{"disabled", 0, time.Second, nil, "success", 0},
		{"slow_failure", time.Millisecond, time.Second, context.DeadlineExceeded, "error", 1},
		{"slow_not_found", time.Millisecond, time.Second, errors.Join(errors.New("query"), gorm.ErrRecordNotFound), "not_found", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			metrics, err := newSQLMetrics(registry)
			if err != nil {
				t.Fatal(err)
			}
			metrics.record("primary", "query", tc.threshold, tc.elapsed, tc.err)
			families, err := registry.Gather()
			if err != nil {
				t.Fatal(err)
			}
			var count, slow float64
			for _, family := range families {
				for _, label := range family.Metric[0].Label {
					if label.GetName() == "result" && label.GetValue() != tc.result {
						t.Errorf("result=%s, want %s", label.GetValue(), tc.result)
					}
				}
				if family.GetName() == "database_sql_operations_total" {
					count = family.Metric[0].GetCounter().GetValue()
				}
				if family.GetName() == "database_sql_slow_operations_total" {
					slow = family.Metric[0].GetCounter().GetValue()
				}
				if family.GetName() == "database_sql_operation_duration_seconds" {
					histogram := family.Metric[0].GetHistogram()
					if histogram.GetSampleCount() != 1 || histogram.GetSampleSum() != tc.elapsed.Seconds() {
						t.Errorf("histogram=%v", histogram)
					}
				}
			}
			if count != 1 || slow != tc.wantSlow {
				t.Errorf("count=%v, slow=%v, want slow=%v", count, slow, tc.wantSlow)
			}
			metrics.unregister(registry)
			families, err = registry.Gather()
			if err != nil || len(families) != 0 {
				t.Fatalf("cleanup families=%v, err=%v", families, err)
			}
		})
	}
}

func TestSQLMetricsEffectiveThreshold(t *testing.T) {
	for _, tc := range []struct {
		name       string
		global     *config_pb.Gorm
		connection *config_pb.Gorm
		want       time.Duration
	}{
		{name: "default", want: 200 * time.Millisecond},
		{name: "global", global: &config_pb.Gorm{Logger: &config_pb.GormLogger{SlowThreshold: durationpb.New(time.Second)}}, want: time.Second},
		{name: "connection", global: &config_pb.Gorm{Logger: &config_pb.GormLogger{SlowThreshold: durationpb.New(time.Second)}}, connection: &config_pb.Gorm{Logger: &config_pb.GormLogger{SlowThreshold: durationpb.New(50 * time.Millisecond)}}, want: 50 * time.Millisecond},
		{name: "connection_zero", connection: &config_pb.Gorm{Logger: &config_pb.GormLogger{SlowThreshold: durationpb.New(0)}}, want: 0},
		{name: "global_zero", global: &config_pb.Gorm{Logger: &config_pb.GormLogger{SlowThreshold: durationpb.New(0)}}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := newManagerMetricsProvider()
			conf := &config_pb.Database{Default: proto.String("primary"), Gorm: tc.global, Connections: map[string]*config_pb.DBConnection{"primary": {Driver: proto.String("sqlite3"), Dsn: ":memory:", Gorm: tc.connection}}}
			driver := new(recordingSQLiteDriver)
			_, cleanup, err := newManagerWithDrivers(newManagerTestLogger(t), testconfig.New(t, "database", conf), managerTestAppInfo{}, newManagerTracingProvider(true), provider, map[string]DriverFactory{"sqlite3": driver.open})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			families, err := provider.registry.Gather()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, family := range families {
				if family.GetName() == "database_sql_slow_threshold_seconds" {
					found = true
					if len(family.Metric) != 1 || family.Metric[0].GetGauge().GetValue() != tc.want.Seconds() {
						t.Errorf("threshold=%v, want %v", family.Metric, tc.want)
					}
				}
			}
			if !found {
				t.Fatal("missing threshold gauge")
			}
		})
	}
}
