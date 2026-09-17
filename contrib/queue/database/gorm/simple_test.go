package gorm

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
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
	if err := store.Enqueue(ctx, &queue.Task{ID: "one", Type: "mail.v1", AvailableAt: now}); err != nil {
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
		if err := store.Enqueue(txCtx, &queue.Task{ID: "one", Type: "message.v1", AvailableAt: time.Now()}); err != nil {
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
	task := &queue.Task{ID: "retained", Type: "mail", Payload: []byte("body"), AvailableAt: now}
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
		CompletedAt   int64
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
	if row.Status != "completed" || row.CompletedAt <= 0 || row.Token != "" || row.ReservedUntil != 0 || row.Attempts != 1 {
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

func TestSimpleRepoMigratesLegacyStates(t *testing.T) {
	_, db := testRepo(t)
	ctx := context.Background()
	// 旧版表结构保留所有原列；测试实际补列与回填，避免仅验证空表迁移。
	type legacyModel struct {
		ID            string `gorm:"column:id;primaryKey;size:256"`
		Identity      string `gorm:"column:identity;size:36;not null"`
		Data          []byte `gorm:"column:data;not null"`
		Attempts      int    `gorm:"column:attempts;not null"`
		Token         string `gorm:"column:token;size:36;not null"`
		AvailableAt   int64  `gorm:"column:available_at;not null;index"`
		ReservedUntil int64  `gorm:"column:reserved_until;not null"`
		Failed        bool   `gorm:"column:failed;not null;index"`
		FailureReason string `gorm:"column:failure_reason;size:128;not null"`
		FailedAt      int64  `gorm:"column:failed_at;not null"`
	}
	const table = "legacy_tasks"
	if err := db.Table(table).AutoMigrate(&legacyModel{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Migrator().DropTable(table); err != nil {
			t.Error(err)
		}
	})
	rows := []legacyModel{
		{ID: "pending", Identity: "one", Data: []byte("pending")},
		{ID: "running", Identity: "two", Data: []byte("running"), Token: "lease", ReservedUntil: 123, Attempts: 2},
		{ID: "failed", Identity: "three", Data: []byte("failed"), Failed: true, FailureReason: "permanent", FailedAt: 456, Attempts: 3},
	}
	if err := db.Table(table).Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	repo, err := NewSimpleRepo(ctx, connectionFunc(func(ctx context.Context) *gorm.DB { return db.WithContext(ctx) }), SimpleConfig{Table: table, RetainCompleted: true})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟 DDL 已成功、回填失败；恢复后重跑必须补齐状态且保留原数据。
	sentinel := errors.New("backfill unavailable")
	if err := db.Callback().Update().Before("gorm:update").Register("test:backfill_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == table {
			tx.AddError(sentinel)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Migrate(ctx); !errors.Is(err, sentinel) {
		t.Fatalf("backfill error not preserved: %v", err)
	}
	if err := db.Callback().Update().Remove("test:backfill_failure"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := repo.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, before := range rows {
		var after simpleModel
		if err := db.Table(table).Where("id = ?", before.ID).Take(&after).Error; err != nil {
			t.Fatal(err)
		}
		if string(after.Status) != before.ID || after.CompletedAt != 0 || string(after.Data) != string(before.Data) || after.Token != before.Token || after.Attempts != before.Attempts || after.FailureReason != before.FailureReason {
			t.Fatalf("migration changed task or lost state: %+v", after)
		}
	}
}
