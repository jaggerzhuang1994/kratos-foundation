package database

import (
	"context"
	"errors"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestContextSelectorsAndTransactionFramesRespectOwnership(t *testing.T) {
	one, two := &manager{}, &manager{}
	first, second := &gorm.DB{}, &gorm.DB{}
	ctx := useTx(useTx(context.Background(), one, first), two, second)
	if got, ok := getTx(ctx, one); !ok || got != first {
		t.Fatal("outer manager transaction not found")
	}
	if got, ok := getTx(ctx, two); !ok || got != second {
		t.Fatal("inner manager transaction not found")
	}
	ctx = UseConnection(ctx, "analytics")
	if name, ok := getConnection(ctx); !ok || name != "analytics" {
		t.Fatal("connection selector missing")
	}

}

func TestTransactionCommitsRollsBackAndRejectsInvalidNestedOptions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:transaction_test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close SQLite test database: %v", err)
		}
	})
	if err := db.Exec("CREATE TABLE records (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	mgr := &manager{db: db}
	if err := mgr.Transaction(context.Background(), nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	if err := mgr.Transaction(context.Background(), func(ctx context.Context) error {
		if _, ok := getTx(ctx, mgr); !ok {
			t.Fatal("transaction missing from context")
		}
		return mgr.Connection(ctx).Exec("INSERT INTO records(id) VALUES (1)").Error
	}); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("rollback")
	if err := mgr.Transaction(context.Background(), func(ctx context.Context) error {
		if err := mgr.Connection(ctx).Exec("INSERT INTO records(id) VALUES (2)").Error; err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("error=%v", err)
	}
	var count int64
	if err := db.Raw("SELECT count(*) FROM records").Scan(&count).Error; err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	nested := useTx(context.Background(), mgr, db)
	if err := mgr.Transaction(nested, func(context.Context) error { return nil }, WithSQLTxOptions(nil)); err == nil {
		t.Fatal("nil SQL options accepted")
	}
}
