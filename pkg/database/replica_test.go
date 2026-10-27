package database

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	queuegorm "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database/gorm"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm/clause"
)

type replicaRecord struct {
	ID     int `gorm:"primaryKey"`
	Source string
}

// newReplicaManager 让主库与副本是两个独立 SQLite 文件，分别写入不同数据以区分语句落点。
func newReplicaManager(t *testing.T) *manager {
	t.Helper()
	dir := t.TempDir()
	conf := &config_pb.Database{Default: proto.String("primary"), Connections: map[string]*config_pb.DBConnection{
		"primary": {
			Driver: proto.String("sqlite3"), Dsn: filepath.Join(dir, "primary.db"), MaxOpenConns: proto.Int32(4),
			ReadReplicas: []*config_pb.DBReplica{{Dsn: filepath.Join(dir, "replica.db")}},
		},
	}}
	m := newSQLiteManager(t, conf)
	for _, name := range []string{"primary", "primary/replica-1"} {
		pool, ok := m.connectionFactory.pool(name)
		if !ok {
			t.Fatalf("pool %s not tracked", name)
		}
		for _, statement := range []string{"CREATE TABLE replica_records (id INTEGER PRIMARY KEY, source TEXT)", "INSERT INTO replica_records (id, source) VALUES (1, '" + name + "')"} {
			if _, err := pool.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
	}
	return m
}

func sourceOf(t *testing.T, m *manager, ctx context.Context, query func(context.Context) (string, error)) string {
	t.Helper()
	source, err := query(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

// 事务外查询走副本；写入、事务、锁定读与 UsePrimary 走主库。
func TestReadReplicasRouteReadsOutsideTransactions(t *testing.T) {
	m := newReplicaManager(t)
	ctx := context.Background()
	first := func(ctx context.Context) (string, error) {
		var record replicaRecord
		err := m.Connection(ctx).First(&record, 1).Error
		return record.Source, err
	}
	raw := func(ctx context.Context) (string, error) {
		var source string
		err := m.Connection(ctx).Raw("SELECT source FROM replica_records WHERE id = 1").Scan(&source).Error
		return source, err
	}
	locking := func(ctx context.Context) (string, error) {
		var record replicaRecord
		err := m.Connection(ctx).Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).First(&record, 1).Error
		return record.Source, err
	}
	for name, tc := range map[string]struct {
		ctx   context.Context
		query func(context.Context) (string, error)
		want  string
	}{
		"query":           {ctx, first, "primary/replica-1"},
		"raw select":      {ctx, raw, "primary/replica-1"},
		"locking read":    {ctx, locking, "primary"},
		"use primary":     {UsePrimary(ctx), first, "primary"},
		"use primary raw": {UsePrimary(ctx), raw, "primary"},
	} {
		if got := sourceOf(t, m, tc.ctx, tc.query); got != tc.want {
			t.Errorf("%s routed to %q, want %q", name, got, tc.want)
		}
	}
	if err := m.Connection(ctx).Create(&replicaRecord{ID: 2, Source: "written"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := m.Connection(ctx).Exec("UPDATE replica_records SET source = 'updated' WHERE id = 1").Error; err != nil {
		t.Fatal(err)
	}
	primary, _ := m.connectionFactory.pool("primary")
	replica, _ := m.connectionFactory.pool("primary/replica-1")
	var primaryRows, replicaRows int
	if err := primary.db.QueryRow("SELECT COUNT(*) FROM replica_records WHERE source IN ('written', 'updated')").Scan(&primaryRows); err != nil {
		t.Fatal(err)
	}
	if err := replica.db.QueryRow("SELECT COUNT(*) FROM replica_records").Scan(&replicaRows); err != nil {
		t.Fatal(err)
	}
	if primaryRows != 2 || replicaRows != 1 {
		t.Fatalf("writes reached primary=%d replica rows=%d", primaryRows, replicaRows)
	}
	// 事务内的读写都在主库，读得到本事务刚写入的数据。
	err := m.Transaction(ctx, func(txCtx context.Context) error {
		if err := m.Connection(txCtx).Create(&replicaRecord{ID: 3, Source: "in-tx"}).Error; err != nil {
			return err
		}
		var record replicaRecord
		if err := m.Connection(txCtx).First(&record, 3).Error; err != nil {
			return err
		}
		if got := sourceOf(t, m, txCtx, first); got != "updated" {
			t.Errorf("transaction read routed to %q", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// 副本沿用所属连接的池参数并一起热更新、一起关闭；副本 DSN 必填。
func TestReadReplicaPoolsFollowConnectionLifecycle(t *testing.T) {
	m := newReplicaManager(t)
	replica, _ := m.connectionFactory.pool("primary/replica-1")
	if got := replica.db.Stats().MaxOpenConnections; got != 4 {
		t.Fatalf("replica max open = %d, want inherited 4", got)
	}
	next := &config_pb.Database{Default: proto.String("primary"), Connections: map[string]*config_pb.DBConnection{
		"primary": {Driver: proto.String("sqlite3"), Dsn: "unused", MaxOpenConns: proto.Int32(9)},
	}}
	if applied := applyConnectionPools(m.connectionFactory, next); applied != 2 || replica.db.Stats().MaxOpenConnections != 9 {
		t.Fatalf("applied=%d replica max open=%d", applied, replica.db.Stats().MaxOpenConnections)
	}
	if err := m.close(); err != nil {
		t.Fatal(err)
	}
	if err := replica.db.Ping(); err == nil {
		t.Fatal("replica pool still open after manager close")
	}
	invalid := &config_pb.Database{Default: proto.String("primary"), Connections: map[string]*config_pb.DBConnection{
		"primary": {Driver: proto.String("sqlite3"), Dsn: "x", ReadReplicas: []*config_pb.DBReplica{{Dsn: " "}}},
	}}
	if err := validateConnections(invalid, map[string]DriverFactory{"sqlite3": new(recordingSQLiteDriver).open}); err == nil || !strings.Contains(err.Error(), "read replica 1 DSN is required") {
		t.Fatalf("empty replica dsn = %v", err)
	}
}

// 框架队列仓储在配置副本的 Manager 上始终读写主库：迁移可重复执行，候选与统计不受副本内容影响。
func TestQueueRepoUsesPrimaryWithReadReplicas(t *testing.T) {
	m := newReplicaManager(t)
	ctx := context.Background()
	repo, err := queuegorm.NewSimpleRepo(ctx, m, queuegorm.SimpleConfig{Table: "replica_queue"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := repo.Migrate(ctx); err != nil {
			t.Fatalf("migrate with read replica: %v", err)
		}
	}
	now := time.Now().UTC()
	if err := repo.Insert(ctx, &databasequeue.TaskRecord{Task: queue.Task{ID: "t1", MessageVersion: "job", AvailableAt: now}}); err != nil {
		t.Fatal(err)
	}
	stats, err := repo.Stats(ctx, now.Add(time.Second))
	if err != nil || stats.Ready != 1 {
		t.Fatalf("stats read replica state: %+v %v", stats, err)
	}
	record, err := repo.Claim(ctx, now.Add(time.Second), now.Add(time.Minute), "owner")
	if err != nil || record == nil || record.Task.ID != "t1" {
		t.Fatalf("claim from primary: %+v %v", record, err)
	}
	replica, _ := m.connectionFactory.pool("primary/replica-1")
	var tables int
	if err := replica.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name = 'replica_queue'").Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("queue table reached replica: %d %v", tables, err)
	}
}
