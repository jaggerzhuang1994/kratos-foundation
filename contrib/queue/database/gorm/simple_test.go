package gorm

import (
	"context"
	"errors"
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
