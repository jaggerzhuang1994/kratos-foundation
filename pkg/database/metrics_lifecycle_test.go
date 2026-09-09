package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
)

func TestMySQLDynamicMetricsCleanupAllowsRecreation(t *testing.T) {
	for _, pedantic := range []bool{false, true} {
		t.Run(fmt.Sprintf("pedantic=%t", pedantic), func(t *testing.T) {
			provider := newManagerMetricsProvider()
			if pedantic {
				provider.registry = prometheus.NewPedanticRegistry()
			}
			for generation := 0; generation < 2; generation++ {
				db, script := newMySQLMetricsScriptDB(t, mysqlMetricsQueryStep{rows: [][]driver.Value{{"Uptime", "30"}}})
				factory := &connectionFactory{connections: map[*sql.DB]connectionPool{db: {db: db, name: "primary", driver: "mysql"}}}
				c, err := newMetricsCollector(factory, &config_pb.Database{Default: proto.String("primary")}, managerTestAppInfo{}, newManagerTestLogger(t), provider)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(c.close)
				receiveMySQLMetricsQuery(t, script.calls)
				waitForMySQLMetricsValues(t, c.mysql, map[string]float64{"Uptime": 30})
				if families, err := provider.registry.Gather(); err != nil || len(families) == 0 {
					t.Fatalf("Gather() families=%d err=%v", len(families), err)
				}

				c.close()
				families, err := provider.registry.Gather()
				if err != nil {
					t.Fatal(err)
				}
				for _, f := range families {
					t.Errorf("metric still registered after close: %s", f.GetName())
				}
			}
		})
	}
}

func TestMySQLMetricsRegistrationConflictKeepsExistingCollectorOwnedByCaller(t *testing.T) {
	for _, whitelist := range []bool{false, true} {
		t.Run(fmt.Sprintf("whitelist=%t", whitelist), func(t *testing.T) {
			registry := prometheus.NewPedanticRegistry()
			existing := prometheus.NewGauge(prometheus.GaugeOpts{Name: "gorm_status_Uptime", Help: "MySQL SHOW STATUS variable Uptime."})
			existing.Set(99)
			if err := registry.Register(existing); err != nil {
				t.Fatal(err)
			}
			db, _ := newMySQLMetricsScriptDB(t, mysqlMetricsQueryStep{rows: [][]driver.Value{{"Uptime", "30"}}})
			config := &config_pb.GormMetrics_Mysql{}
			if whitelist {
				config.VariableNames = []string{"Uptime", "Questions"}
			}
			collector, err := newMySQLMetricsCollector(db, time.Hour, config, newManagerTestLogger(t), registry)
			if whitelist {
				if err == nil || collector != nil {
					t.Fatalf("constructor accepted duplicate collector: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				collector.refresh(context.Background())
				collector.close()
			}
			families, err := registry.Gather()
			if err != nil || len(families) != 1 || families[0].GetName() != "gorm_status_Uptime" || families[0].Metric[0].Gauge.GetValue() != 99 {
				t.Fatalf("existing metric ownership changed: families=%v err=%v", families, err)
			}
			if !registry.Unregister(existing) {
				t.Fatal("caller could not unregister its collector")
			}
		})
	}
}
