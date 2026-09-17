package gorm

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"gorm.io/gorm"
)

func TestSimpleRepoIsolationAndMigration(t *testing.T) {
	_, db := testRepo(t)
	ctx := context.Background()
	provider := connectionFunc(func(ctx context.Context) *gorm.DB { return db.WithContext(ctx) })
	first, err := NewSimpleRepo(ctx, provider, SimpleConfig{Table: "simple_mail"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSimpleRepo(ctx, provider, SimpleConfig{Table: "simple_bot"})
	if err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasTable("simple_mail") {
		t.Fatal("constructor migrated table")
	}
	for _, repo := range []*SimpleRepo{first, second} {
		if err := repo.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		if err := repo.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		columns, err := db.Table(repo.table).Migrator().ColumnTypes(&simpleModel{})
		if err != nil {
			t.Fatal(err)
		}
		columnTypes := make(map[string]string, len(columns))
		for _, column := range columns {
			columnTypes[column.Name()] = column.DatabaseTypeName()
		}
		if _, ok := columnTypes["generation"]; !ok {
			t.Fatal("missing internal generation column")
		}
		if _, ok := columnTypes["identity"]; ok {
			t.Fatal("legacy identity column still exists")
		}
		for _, name := range []string{"available_at", "failed_at", "completed_at"} {
			dataType := strings.ToLower(columnTypes[name])
			if !strings.HasPrefix(dataType, "timestamp") {
				t.Fatalf("%s type = %q, want timestamp", name, columnTypes[name])
			}
		}
		// 验证实际落库的列顺序和按表命名，覆盖嵌入模型、多表及重复迁移。
		indexes, err := db.Table(repo.table).Migrator().GetIndexes(&simpleModel{})
		if err != nil {
			t.Fatal(err)
		}
		for suffix, columns := range map[string][]string{
			"queue_failed": {"failed", "failed_at", "id"},
			"queue_stats":  {"status", "failed", "available_at", "reserved_until"},
		} {
			name := db.NamingStrategy.IndexName(repo.table, suffix)
			found := false
			for _, index := range indexes {
				if index.Name() == name {
					found = true
					if !reflect.DeepEqual(index.Columns(), columns) {
						t.Fatalf("%s columns = %v, want %v", name, index.Columns(), columns)
					}
				}
			}
			if !found {
				t.Fatalf("missing index %s", name)
			}
		}
		t.Cleanup(func() {
			if err := db.Migrator().DropTable(repo.table); err != nil {
				t.Error(err)
			}
		})
	}
	store := databasequeue.NewStore(first)
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := store.Enqueue(ctx, &queue.Task{ID: "one", MessageVersion: "mail.v1", AvailableAt: now}); err != nil {
		t.Fatal(err)
	}
	if got, err := databasequeue.NewStore(second).Reserve(ctx, now, time.Second); err != nil || got != nil {
		t.Fatalf("cross-table claim: %+v %v", got, err)
	}
	reservation, err := store.Reserve(ctx, now, time.Second)
	if err != nil || reservation == nil {
		t.Fatalf("claim: %+v %v", reservation, err)
	}
	if err := store.Fail(ctx, reservation, "permanent", now); err != nil {
		t.Fatal(err)
	}
	failed, err := store.Failed(ctx, 10)
	if err != nil || len(failed) != 1 || failed[0].Attempts != 1 {
		t.Fatalf("failed: %+v %v", failed, err)
	}
	if err := store.Retry(ctx, "one", now); err != nil {
		t.Fatal(err)
	}
	reservation, err = store.Reserve(ctx, now, time.Second)
	if err != nil || reservation == nil || reservation.Attempts != 1 {
		t.Fatalf("retry: %+v %v", reservation, err)
	}
	if err := store.Ack(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"", "mail;drop table tasks", "schema.mail", " mail"} {
		if _, err := NewSimpleRepo(ctx, provider, SimpleConfig{Table: table}); err == nil {
			t.Errorf("accepted table %q", table)
		}
	}
}

func TestSimpleRepoTransactionOwnership(t *testing.T) {
	_, db := testRepo(t)
	type transactionKey struct{}
	provider := connectionFunc(func(ctx context.Context) *gorm.DB {
		if tx, ok := ctx.Value(transactionKey{}).(*gorm.DB); ok {
			return tx.WithContext(ctx)
		}
		return db.WithContext(ctx)
	})
	ctx := context.Background()
	repo, err := NewSimpleRepo(ctx, provider, SimpleConfig{Table: "simple_transaction"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Migrator().DropTable("simple_transaction"); err != nil {
			t.Error(err)
		}
	})
	store := databasequeue.NewStore(repo)
	rollback := errors.New("rollback business transaction")
	err = db.Transaction(func(tx *gorm.DB) error {
		txCtx := context.WithValue(ctx, transactionKey{}, tx)
		if err := repo.Migrate(txCtx); err == nil {
			return errors.New("migration accepted outer transaction")
		}
		if err := store.Enqueue(txCtx, &queue.Task{ID: "one", MessageVersion: "message.v1", AvailableAt: time.Now()}); err != nil {
			return err
		}
		var count int64
		if err := tx.Table("simple_transaction").Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return errors.New("enqueue did not join transaction")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	var count int64
	if err := db.Table("simple_transaction").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("task survived business rollback")
	}
}

func TestSimpleRepoRetainsCompletedTasks(t *testing.T) {
	_, db := testRepo(t)
	ctx := context.Background()
	var config SimpleConfig
	if err := json.Unmarshal([]byte(`{"table":"retained_tasks","retain_completed":true}`), &config); err != nil {
		t.Fatal(err)
	}
	repo, err := NewSimpleRepo(ctx, connectionFunc(func(ctx context.Context) *gorm.DB { return db.WithContext(ctx) }), config)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Migrator().DropTable(config.Table); err != nil {
			t.Error(err)
		}
	})
	store := databasequeue.NewStore(repo)
	now := time.Unix(1700000000, 0).UTC()
	task := &queue.Task{ID: "retained", MessageVersion: "mail", Payload: []byte("body"), AvailableAt: now}
	if err := store.Enqueue(ctx, task); err != nil {
		t.Fatal(err)
	}
	reservation, err := store.Reserve(ctx, now, time.Minute)
	if err != nil || reservation == nil {
		t.Fatalf("reserve: %v %v", reservation, err)
	}
	if err := store.Ack(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Table(config.Table).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("completed task count = %d, want retained record", count)
	}
	var row struct {
		Status        string
		CompletedAt   *time.Time
		Token         string
		ReservedUntil int64
		Attempts      int
	}
	if err := db.Table(config.Table).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if row.Status != "completed" || row.CompletedAt == nil || row.Token != "" || row.ReservedUntil != 0 || row.Attempts != 1 {
		t.Fatalf("completed state: %+v", row)
	}
	if got, err := store.Reserve(ctx, now.Add(time.Hour), time.Minute); err != nil || got != nil {
		t.Fatalf("completed task reclaimed: %v %v", got, err)
	}
	if err := store.Enqueue(ctx, task); !errors.Is(err, queue.ErrDuplicate) {
		t.Fatalf("completed id reused: %v", err)
	}
	stats, err := repo.Stats(ctx, now.Add(time.Hour))
	if err != nil || stats.Ready != 0 || stats.Scheduled != 0 || stats.Running != 0 || stats.Failed != 0 || !stats.OldestReadyAt.IsZero() {
		t.Fatalf("completed task counted as backlog: %+v %v", stats, err)
	}
}
