package mysql

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
)

// TestExternalMySQLReadReplicas 用同一 MySQL 的两个库模拟主库与副本，验证真实 MySQL 方言下的读写分流。
func TestExternalMySQLReadReplicas(t *testing.T) {
	dsn := os.Getenv("FOUNDATION_TEST_MYSQL_DSN")
	if dsn == "" || !strings.Contains(dsn, "/foundation_test?") {
		t.Skip("set FOUNDATION_TEST_MYSQL_DSN (database foundation_test) for Docker integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := newConnection(database.DriverConfig{Name: "admin", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.SQLDB.Close(); err != nil {
			t.Error(err)
		}
	})
	suffix := fmt.Sprint(time.Now().UnixNano())
	replicaSchema, table := "foundation_replica_"+suffix, "replica_records_"+suffix
	replicaDSN := strings.Replace(dsn, "/foundation_test?", "/"+replicaSchema+"?", 1)
	for _, statement := range []string{
		"CREATE DATABASE " + replicaSchema,
		"CREATE TABLE " + table + " (id BIGINT PRIMARY KEY, source VARCHAR(32))",
		"INSERT INTO " + table + " VALUES (1, 'primary')",
		"CREATE TABLE " + replicaSchema + "." + table + " (id BIGINT PRIMARY KEY, source VARCHAR(32))",
		"INSERT INTO " + replicaSchema + "." + table + " VALUES (1, 'replica')",
	} {
		if _, err := admin.SQLDB.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = admin.SQLDB.Exec("DROP TABLE IF EXISTS " + table)
		_, _ = admin.SQLDB.Exec("DROP DATABASE IF EXISTS " + replicaSchema)
	})
	logger, cleanupLog, err := foundationlog.NewLogger()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupLog)
	info := appinfo.New("mysql-replica-test")
	metricsProvider, cleanupMetrics, err := metrics.NewProvider(info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupMetrics)
	tracingProvider, cleanupTracing, err := tracing.NewProvider(testconfig.Empty(t), info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupTracing)
	manager, cleanup, err := database.NewManager(logger, testconfig.New(t, "database", &config_pb.Database{
		Default: proto.String("primary"),
		Connections: map[string]*config_pb.DBConnection{"primary": {
			Driver: proto.String(DriverName), Dsn: dsn, ReadReplicas: []*config_pb.DBReplica{{Dsn: replicaDSN}},
		}},
	}), info, tracingProvider, metricsProvider)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	read := func(ctx context.Context) string {
		t.Helper()
		var source string
		if err := manager.Connection(ctx).Table(table).Where("id = ?", 1).Pluck("source", &source).Error; err != nil {
			t.Fatal(err)
		}
		return source
	}
	if got := read(ctx); got != "replica" {
		t.Fatalf("read outside transaction routed to %q", got)
	}
	if got := read(database.UsePrimary(ctx)); got != "primary" {
		t.Fatalf("UsePrimary read routed to %q", got)
	}
	if err := manager.Connection(ctx).Exec("UPDATE " + table + " SET source = 'written' WHERE id = 1").Error; err != nil {
		t.Fatal(err)
	}
	err = manager.Transaction(ctx, func(txCtx context.Context) error {
		if got := read(txCtx); got != "written" {
			t.Errorf("transaction read routed to %q", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var replicaSource string
	if err := admin.SQLDB.QueryRowContext(ctx, "SELECT source FROM "+replicaSchema+"."+table+" WHERE id = 1").Scan(&replicaSource); err != nil || replicaSource != "replica" {
		t.Fatalf("write reached replica: %q %v", replicaSource, err)
	}
}
