package gorm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type testTask struct {
	Model
	TenantID string
	OrderID  string
}

func (*testTask) TableName() string { return "queue_test_tasks" }

type connectionFunc func(context.Context) *gorm.DB

func (f connectionFunc) Connection(ctx context.Context) *gorm.DB { return f(ctx) }

func testRepo(t *testing.T) (*Repo[*testTask], *gorm.DB) {
	t.Helper()
	dialector := sqlite.Open(filepath.Join(t.TempDir(), "queue.db") + "?_journal_mode=WAL&_busy_timeout=5000")
	if dsn := os.Getenv("FOUNDATION_TEST_QUEUE_MYSQL_DSN"); dsn != "" {
		dialector = mysql.Open(dsn)
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
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
	if db.Migrator().HasTable(&testTask{}) {
		t.Fatal("test queue table already exists; use an isolated database")
	}
	t.Cleanup(func() {
		if err := db.Migrator().DropTable(&testTask{}); err != nil {
			t.Error(err)
		}
	})
	if err := db.AutoMigrate(&testTask{}); err != nil {
		t.Fatal(err)
	}
	repo, err := NewRepo(context.Background(), connectionFunc(func(ctx context.Context) *gorm.DB { return db.WithContext(ctx) }), func(_ context.Context, r *databasequeue.TaskRecord) (*testTask, error) {
		return &testTask{TenantID: "tenant", OrderID: r.Task.ID}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return repo, db
}

func TestRepoLifecycle(t *testing.T) {
	repo, db := testRepo(t)
	var _ databasequeue.Repo = repo
	store := databasequeue.NewStore(repo)
	ctx := context.Background()
	now := time.Unix(1700000000, 123456)
	task := &queue.Task{ID: "任务A ", Type: "email", Payload: []byte{0, 255}, Headers: map[string]string{"k": "v"}, AvailableAt: now}
	if err := store.Enqueue(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := store.Enqueue(ctx, task); !errors.Is(err, queue.ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	if r, err := store.Reserve(ctx, now, time.Second); err != nil || r != nil {
		t.Fatalf("early claim: %v %v", r, err)
	}
	now = now.Add(time.Millisecond)
	first, err := store.Reserve(ctx, now, time.Second)
	if err != nil || first == nil {
		t.Fatalf("claim: %v %v", first, err)
	}
	if first.Attempts != 1 || string(first.Task.Payload) != string(task.Payload) {
		t.Fatal(first)
	}
	if r, err := store.Reserve(ctx, now, time.Second); err != nil || r != nil {
		t.Fatalf("active lease claimed: %v %v", r, err)
	}
	second, err := store.Reserve(ctx, now.Add(2*time.Second), time.Second)
	if err != nil || second == nil || second.Attempts != 2 {
		t.Fatalf("reclaim: %v %v", second, err)
	}
	for _, operation := range []func() error{func() error { return store.Ack(ctx, first) }, func() error { return store.Release(ctx, first, now) }, func() error { return store.Fail(ctx, first, "old", now) }} {
		if err := operation(); !errors.Is(err, queue.ErrLeaseLost) {
			t.Fatalf("stale token: %v", err)
		}
	}
	if err := store.Release(ctx, second, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	third, err := store.Reserve(ctx, now.Add(6*time.Second), time.Second)
	if err != nil || third == nil || third.Attempts != 3 {
		t.Fatalf("release: %v %v", third, err)
	}
	if err := store.Fail(ctx, third, "test", now); err != nil {
		t.Fatal(err)
	}
	failed, err := store.Failed(ctx, 10)
	if err != nil || len(failed) != 1 || failed[0].Reason != "test" {
		t.Fatalf("failed: %v %v", failed, err)
	}
	if err := store.Retry(ctx, task.ID, now); err != nil {
		t.Fatal(err)
	}
	last, err := store.Reserve(ctx, now.Add(time.Second), time.Second)
	if err != nil || last == nil || last.Attempts != 1 {
		t.Fatalf("retry: %v %v", last, err)
	}
	var saved testTask
	if err := db.First(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.TenantID != "tenant" || saved.OrderID != task.ID {
		t.Fatal("custom columns changed")
	}
	if err := store.Ack(ctx, last); err != nil {
		t.Fatal(err)
	}
	if err := store.Retry(ctx, task.ID, now); !errors.Is(err, queue.ErrNotFound) {
		t.Fatal(err)
	}
	if err := store.Enqueue(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := store.Ack(ctx, last); !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatal("old owner deleted new task")
	}
}

func TestRepoConcurrentClaim(t *testing.T) {
	repo, _ := testRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := repo.Insert(ctx, &databasequeue.TaskRecord{Task: queue.Task{ID: "one", Type: "test", AvailableAt: now}}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan *databasequeue.TaskRecord, 16)
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			record, err := repo.Claim(ctx, now.Add(time.Second), now.Add(time.Hour), fmt.Sprint(i))
			if err != nil {
				t.Error(err)
			}
			if record != nil {
				results <- record
			}
		}()
	}
	wg.Wait()
	close(results)
	if len(results) != 1 {
		t.Fatalf("owners: %d", len(results))
	}
}

func TestRepoTransactionsAndErrors(t *testing.T) {
	repo, db := testRepo(t)
	ctx := context.Background()
	sentinel := errors.New("rollback")
	for _, rollback := range []bool{true, false} {
		err := db.Transaction(func(tx *gorm.DB) error {
			transactional, err := NewRepo(ctx, connectionFunc(func(context.Context) *gorm.DB { return tx }), repo.factory)
			if err != nil {
				return err
			}
			if _, err := transactional.Claim(ctx, time.Now(), time.Now().Add(time.Hour), "t"); err == nil {
				t.Fatal("uncommitted lease allowed")
			}
			if err := transactional.Insert(ctx, &databasequeue.TaskRecord{Task: queue.Task{ID: "tx", Type: "test"}}); err != nil {
				return err
			}
			var count int64
			if err := db.Model(&testTask{}).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				t.Fatal("uncommitted task visible")
			}
			if rollback {
				return sentinel
			}
			return nil
		})
		if rollback && !errors.Is(err, sentinel) || !rollback && err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Insert(ctx, nil); err == nil {
		t.Fatal("nil input")
	}
	if _, err := repo.Claim(ctx, time.Now(), time.Now(), ""); err == nil {
		t.Fatal("invalid claim")
	}
	if _, err := repo.ListFailed(ctx, 0); err == nil {
		t.Fatal("invalid limit")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repo.Claim(cancelled, time.Now(), time.Now().Add(time.Hour), "t"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := repo.ListFailed(cancelled, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := repo.RetryFailed(cancelled, "tx", time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRepoCorruptPayload(t *testing.T) {
	repo, db := testRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := repo.Insert(ctx, &databasequeue.TaskRecord{Task: queue.Task{ID: "bad", Type: "test"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&testTask{}).Where("id = ?", taskKey("bad")).Update("data", []byte("broken")).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Claim(ctx, now, now.Add(time.Second), "t"); err == nil {
		t.Fatal("corrupt payload accepted")
	}
	if record, err := repo.Claim(ctx, now, now.Add(time.Second), "t2"); err != nil || record != nil {
		t.Fatalf("quarantine: %v %v", record, err)
	}
	if _, err := repo.ListFailed(ctx, 1); err == nil {
		t.Fatal("corruption hidden")
	}
	var stored testTask
	if err := db.First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if string(stored.Data) != "broken" || !stored.Failed {
		t.Fatal("corrupt data discarded")
	}
}

type shadowTask struct {
	Model
	ID string
}

func (*shadowTask) TableName() string { return "shadow_tasks" }

type prefixTask struct {
	Model `gorm:"embeddedPrefix:q_"`
}

func (*prefixTask) TableName() string { return "prefix_tasks" }

type softTask struct {
	Model
	DeletedAt gorm.DeletedAt
}

func (*softTask) TableName() string { return "soft_tasks" }

type extraKeyTask struct {
	Model
	Extra string `gorm:"primaryKey"`
}

func (*extraKeyTask) TableName() string { return "extra_tasks" }

func TestRepoRejectsModelOverrides(t *testing.T) {
	_, db := testRepo(t)
	ctx := context.Background()
	provider := connectionFunc(func(context.Context) *gorm.DB { return db })
	if _, err := NewRepo(ctx, provider, func(context.Context, *databasequeue.TaskRecord) (*shadowTask, error) { return &shadowTask{}, nil }); err == nil {
		t.Fatal("shadow accepted")
	}
	if _, err := NewRepo(ctx, provider, func(context.Context, *databasequeue.TaskRecord) (*prefixTask, error) { return &prefixTask{}, nil }); err == nil {
		t.Fatal("prefix accepted")
	}
	if _, err := NewRepo(ctx, provider, func(context.Context, *databasequeue.TaskRecord) (*softTask, error) { return &softTask{}, nil }); err == nil {
		t.Fatal("soft delete accepted")
	}
	if _, err := NewRepo(ctx, provider, func(context.Context, *databasequeue.TaskRecord) (*extraKeyTask, error) { return &extraKeyTask{}, nil }); err == nil {
		t.Fatal("second key accepted")
	}
	if _, err := NewRepo[*testTask](ctx, provider, nil); err == nil {
		t.Fatal("nil factory accepted")
	}
}

func TestRepoFactoryAndIdentity(t *testing.T) {
	repo, db := testRepo(t)
	ctx := context.Background()
	record := &databasequeue.TaskRecord{Task: queue.Task{ID: "id", Type: "test"}}
	sentinel := errors.New("factory failed")
	repo.factory = func(context.Context, *databasequeue.TaskRecord) (*testTask, error) { return nil, sentinel }
	if err := repo.Insert(ctx, record); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	repo.factory = func(context.Context, *databasequeue.TaskRecord) (*testTask, error) { return nil, nil }
	if err := repo.Insert(ctx, record); err == nil {
		t.Fatal("nil model accepted")
	}
	repo.factory = func(_ context.Context, r *databasequeue.TaskRecord) (*testTask, error) {
		r.Task.Type = "changed"
		return &testTask{}, nil
	}
	for _, id := range []string{"id", "ID", "id ", "é", "e"} {
		record.Task.ID = id
		if err := repo.Insert(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := db.Model(&testTask{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatal(count)
	}
	got, err := repo.Claim(ctx, time.Now(), time.Now().Add(time.Hour), "t")
	if err != nil || got == nil || got.Task.Type != "test" {
		t.Fatalf("factory changed payload: %v %v", got, err)
	}
}

func TestRepoClaimRejectsRecreatedTask(t *testing.T) {
	repo, db := testRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	record := &databasequeue.TaskRecord{Task: queue.Task{ID: "same", Type: "test"}}
	if err := repo.Insert(ctx, record); err != nil {
		t.Fatal(err)
	}
	replaced := false
	if err := db.Callback().Update().Before("gorm:begin_transaction").Register("test:recreate", func(tx *gorm.DB) {
		if replaced {
			return
		}
		replaced = true
		if err := db.Where("id = ?", taskKey("same")).Delete(&testTask{}).Error; err != nil {
			t.Error(err)
			return
		}
		if err := repo.Insert(ctx, record); err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Claim(ctx, now, now.Add(time.Hour), "old"); err != nil || got != nil {
		t.Fatalf("stale snapshot claimed new task: %v %v", got, err)
	}
	if got, err := repo.Claim(ctx, now, now.Add(time.Hour), "new"); err != nil || got == nil || got.Attempts != 1 {
		t.Fatalf("new task unavailable: %v %v", got, err)
	}
}

// integrationQueueStore 仅在真实存储完成状态变更后发出信号，不替换持久化或租约逻辑。
type integrationQueueStore struct {
	queue.Store
	settled chan error
}

func (s *integrationQueueStore) Ack(ctx context.Context, r *queue.Reservation) error {
	err := s.Store.Ack(ctx, r)
	s.settled <- err
	return err
}

func (s *integrationQueueStore) Fail(ctx context.Context, r *queue.Reservation, reason string, at time.Time) error {
	err := s.Store.Fail(ctx, r, reason, at)
	s.settled <- err
	return err
}

func integrationQueueObservability(t *testing.T) queue.Observability {
	t.Helper()
	logger, cleanupLog, err := testlog.New(testlog.Config{
		Level: kratoslog.LevelInfo, TimeFormat: time.RFC3339,
		Std:  testlog.OutputConfig{Disable: true, Level: kratoslog.LevelInfo},
		File: testlog.FileConfig{OutputConfig: testlog.OutputConfig{Disable: true, Level: kratoslog.LevelInfo}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupLog)
	info := appinfo.New("queue-integration")
	mp, cleanupMetrics, err := metrics.NewProvider(info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupMetrics)
	tp, cleanupTracing, err := tracing.NewProvider(testconfig.Empty(t), info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupTracing)
	return queue.Observability{Logger: logger, Metrics: mp, Tracing: tp}
}

// TestIntegrationQueuePersistentWorker 贯通业务投递、真实 SQLite 租约、处理器与失败重放。
func TestIntegrationQueuePersistentWorker(t *testing.T) {
	for _, tc := range []struct {
		name     string
		wantRuns int
		reason   string
	}{
		{name: "success", wantRuns: 1},
		{name: "transient_then_success", wantRuns: 2},
		{name: "retry_exhausted", wantRuns: 2, reason: "retry_exhausted"},
		{name: "permanent", wantRuns: 1, reason: "permanent"},
		{name: "panic", wantRuns: 2, reason: "retry_exhausted"},
		{name: "unknown_type", wantRuns: 0, reason: "permanent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 独立 SQLite 文件避免借用开发机器的 MySQL，测试不依赖外部基础设施。
			t.Setenv("FOUNDATION_TEST_QUEUE_MYSQL_DSN", "")
			repo, db := testRepo(t)
			store := &integrationQueueStore{Store: databasequeue.NewStore(repo), settled: make(chan error, 1)}
			obs := integrationQueueObservability(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			original := &queue.Task{ID: "order-123", Type: "email.v1", Payload: []byte("invoice"), Headers: map[string]string{"tenant": "acme"}}
			if tc.name == "unknown_type" {
				original.Type = "missing.v1"
			}
			publisher, err := queue.NewQueue(queue.Definition[[]byte]{Queue: "email", MessageType: strings.TrimSuffix(original.Type, ".v1"), Version: 1, Codec: payloadCodec{}}, store, obs)
			if err != nil {
				t.Fatal(err)
			}
			if id, err := publisher.PostWith(ctx, original.Payload, queue.PostOptions{ID: original.ID, Headers: original.Headers}); err != nil || id != original.ID {
				t.Fatalf("dispatch id=%q error=%v", id, err)
			}
			if _, err := publisher.PostWith(ctx, original.Payload, queue.PostOptions{ID: original.ID, Headers: original.Headers}); !errors.Is(err, queue.ErrDuplicate) {
				t.Fatalf("duplicate dispatch = %v", err)
			}
			original.Payload[0] = 'X'
			original.Headers["tenant"] = "changed"
			runs := 0
			handler := func(_ context.Context, payload []byte) error {
				runs++
				if string(payload) != "invoice" {
					t.Errorf("persisted payload mutated across dispatch/retry: %s", payload)
				}
				// Handler 的写入不能污染下一次重试或失败归档的数据。
				payload[0] = 'Y'
				switch tc.name {
				case "transient_then_success":
					if runs == 1 {
						return errors.New("temporary service failure")
					}
				case "retry_exhausted":
					return errors.New("sensitive backend detail")
				case "permanent":
					return queue.Permanent(errors.New("sensitive input detail"))
				case "panic":
					panic("sensitive panic detail")
				}
				return nil
			}
			runWorker := func(messageType string, handle func(context.Context, []byte) error) {
				t.Helper()
				q, err := queue.NewQueue(queue.Definition[[]byte]{Queue: "email", MessageType: messageType, Version: 1, Codec: payloadCodec{}}, store, obs)
				if err != nil {
					t.Fatal(err)
				}
				worker, err := q.Worker(handle, queue.WorkerConfig{Name: "mailer", PollInterval: time.Millisecond, Retry: &queue.RetryPolicy{MaxAttempts: 2}})
				if err != nil {
					t.Fatal(err)
				}
				runCtx, cancelRun := context.WithCancel(ctx)
				done := make(chan error, 1)
				joined := false
				go func() { done <- worker.Start(runCtx) }()
				defer func() {
					cancelRun()
					// 测试请求超时不应耗尽资源清理窗口；任何断言退出都先等待 Worker 收敛。
					stopCtx, cancelStop := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancelStop()
					if err := worker.Stop(stopCtx); err != nil {
						t.Error(err)
					}
					if !joined {
						select {
						case err := <-done:
							if err != nil {
								t.Error(err)
							}
						case <-stopCtx.Done():
							t.Fatal("worker did not exit within cleanup timeout")
						}
					}
				}()
				select {
				case err := <-store.settled:
					if err != nil {
						t.Fatal(err)
					}
				case err := <-done:
					joined = true
					t.Fatalf("worker exited before terminal state: %v", err)
				case <-ctx.Done():
					t.Fatal("worker did not persist terminal state")
				}
			}
			runWorker("email", handler)
			if runs != tc.wantRuns {
				t.Fatalf("handler runs=%d want=%d", runs, tc.wantRuns)
			}
			failed, err := store.Failed(ctx, 10)
			if err != nil {
				t.Fatal(err)
			}
			if tc.reason != "" {
				if len(failed) != 1 || failed[0].Reason != tc.reason || failed[0].Attempts != max(1, tc.wantRuns) || string(failed[0].Task.Payload) != "invoice" {
					t.Fatalf("failed snapshot = %+v", failed)
				}
				if err := store.Retry(ctx, original.ID, time.Now().Add(-time.Second)); err != nil {
					t.Fatal(err)
				}
				runWorker(strings.TrimSuffix(original.Type, ".v1"), func(context.Context, []byte) error { return nil })
			} else if len(failed) != 0 {
				t.Fatalf("successful task archived: %+v", failed)
			}
			var count int64
			if err := db.Model(&testTask{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("completed task not deleted: count=%d error=%v", count, err)
			}
			if err := store.Retry(ctx, original.ID, time.Now()); !errors.Is(err, queue.ErrNotFound) {
				t.Fatalf("retry completed task = %v", err)
			}
		})
	}
}

// payloadCodec 保留此存储回归用例的原始字节，验证处理器不能改写持久化快照。
type payloadCodec struct{}

func (payloadCodec) Encode(value []byte) ([]byte, error) { return value, nil }
func (payloadCodec) Decode(value []byte) ([]byte, error) { return value, nil }
