package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
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

// queueTransactionRepo 是业务 Repo 的最小测试替身，只实现事务投递需要的 Insert。
// 实际消费方法仍须由业务实现并分别保证提交和租约条件。
type queueTransactionRepo struct {
	databasequeue.Repo
	manager Manager
}

func (r queueTransactionRepo) Insert(ctx context.Context, record *databasequeue.TaskRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return r.manager.Connection(ctx).Exec("INSERT INTO queue_tasks (id, payload) VALUES (?, ?)", record.Task.ID, string(payload)).Error
}

func TestTransactionEnqueuesTaskWithBusinessWrite(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(fmt.Sprintf("rollback=%t", rollback), func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "outbox.db")), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := sqlDB.Close(); err != nil {
					t.Error(err)
				}
			})
			// WAL 允许独立连接在写事务期间读取已提交快照，验证任务不会提前可见。
			for _, statement := range []string{
				"PRAGMA journal_mode=WAL",
				"CREATE TABLE orders (id TEXT PRIMARY KEY)",
				"CREATE TABLE queue_tasks (id TEXT PRIMARY KEY, payload TEXT NOT NULL)",
			} {
				if err := db.Exec(statement).Error; err != nil {
					t.Fatal(err)
				}
			}
			mgr := &manager{db: db}
			store := databasequeue.NewStore(queueTransactionRepo{manager: mgr})
			rejected := errors.New("business rejected")
			err = mgr.Transaction(context.Background(), func(ctx context.Context) error {
				if err := mgr.Connection(ctx).Exec("INSERT INTO orders (id) VALUES (?)", "order-1").Error; err != nil {
					return err
				}
				if err := store.Enqueue(ctx, &queue.Task{ID: "order-1", MessageVersion: "order.created"}); err != nil {
					return err
				}
				var inside, outside int64
				if err := mgr.Connection(ctx).Table("queue_tasks").Count(&inside).Error; err != nil {
					return err
				}
				if err := db.Table("queue_tasks").Count(&outside).Error; err != nil {
					return err
				}
				if inside != 1 || outside != 0 {
					t.Fatalf("transaction visibility: inside=%d outside=%d", inside, outside)
				}
				if rollback {
					return rejected
				}
				return nil
			})
			if rollback && !errors.Is(err, rejected) || !rollback && err != nil {
				t.Fatalf("transaction result: %v", err)
			}
			want := int64(1)
			if rollback {
				want = 0
			}
			for _, table := range []string{"orders", "queue_tasks"} {
				var count int64
				if err := db.Table(table).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != want {
					t.Fatalf("%s: got %d want %d", table, count, want)
				}
			}
		})
	}
}

// TestIntegrationTransactionSavepoints 验证真实 SQLite 中嵌套事务的持久化结果。
func TestIntegrationTransactionSavepoints(t *testing.T) {
	rejected := errors.New("business rejected")
	for _, tt := range []struct {
		name                                                            string
		innerFailure, outerFailure, duplicate, panicInner, cancelBefore bool
		want                                                            []int
	}{
		{name: "nested commit", want: []int{1, 2, 3}},
		{name: "inner rollback and continue", innerFailure: true, want: []int{1, 3}},
		{name: "outer rollback includes inner commit", outerFailure: true},
		{name: "both rollback", innerFailure: true, outerFailure: true},
		{name: "constraint failure rolls back savepoint", duplicate: true, want: []int{1, 3}},
		{name: "recovered inner panic", panicInner: true, want: []int{1, 3}},
		{name: "canceled before begin", cancelBefore: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "transactions.db")), &gorm.Config{TranslateError: true})
			if err != nil {
				t.Fatal(err)
			}
			pool, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := pool.Close(); err != nil {
					t.Error(err)
				}
			})
			if err := db.Exec("CREATE TABLE records (id INTEGER PRIMARY KEY)").Error; err != nil {
				t.Fatal(err)
			}
			var mgr Manager = &manager{db: db}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.cancelBefore {
				cancel()
			}
			entered := false
			err = mgr.Transaction(ctx, func(ctx context.Context) error {
				entered = true
				if err := mgr.Connection(ctx).Exec("INSERT INTO records VALUES (1)").Error; err != nil {
					return err
				}
				var innerErr error
				var recovered any
				func() {
					defer func() { recovered = recover() }()
					innerErr = mgr.Transaction(ctx, func(inner context.Context) error {
						if err := mgr.Connection(inner).Exec("INSERT INTO records VALUES (2)").Error; err != nil {
							return err
						}
						if tt.panicInner {
							panic(rejected)
						}
						if tt.duplicate {
							return mgr.Connection(inner).Exec("INSERT INTO records VALUES (1)").Error
						}
						if tt.innerFailure {
							return rejected
						}
						return nil
					})
				}()
				if tt.panicInner {
					if recovered != rejected {
						t.Fatalf("panic = %v", recovered)
					}
				} else if recovered != nil {
					t.Fatalf("unexpected panic: %v", recovered)
				}
				switch {
				case tt.duplicate:
					if !errors.Is(innerErr, gorm.ErrDuplicatedKey) {
						t.Fatalf("duplicate error = %v", innerErr)
					}
				case tt.innerFailure:
					if !errors.Is(innerErr, rejected) {
						t.Fatalf("inner error = %v", innerErr)
					}
				default:
					if innerErr != nil {
						t.Fatal(innerErr)
					}
				}
				// 内层失败可由业务处理后继续外层；这里只验证既有 savepoint 策略。
				if err := mgr.Connection(ctx).Exec("INSERT INTO records VALUES (3)").Error; err != nil {
					return err
				}
				if tt.outerFailure {
					return rejected
				}
				return nil
			})
			switch {
			case tt.cancelBefore:
				if !errors.Is(err, context.Canceled) || entered {
					t.Fatalf("canceled transaction: entered=%t err=%v", entered, err)
				}
			case tt.outerFailure:
				if !errors.Is(err, rejected) {
					t.Fatalf("outer error = %v", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			var ids []int
			if err := mgr.Connection(t.Context()).Table("records").Order("id").Pluck("id", &ids).Error; err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(ids, tt.want) {
				t.Fatalf("persisted IDs = %v, want %v", ids, tt.want)
			}
		})
	}
}
