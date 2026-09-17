package gorm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	return repo, db
}

func TestRepoTransactionsAndErrors(t *testing.T) {
	repo, db := testRepo(t)
	ctx := context.Background()
	sentinel := errors.New("rollback")
	for _, rollback := range []bool{true, false} {
		err := db.Transaction(func(tx *gorm.DB) error {
			transactional, err := NewRepo(ctx, connectionFunc(func(context.Context) *gorm.DB { return tx }), repo.factory, Config{})
			if err != nil {
				return err
			}
			if _, err := transactional.Claim(ctx, time.Now(), time.Now().Add(time.Hour), "t"); err == nil {
				t.Fatal("uncommitted lease allowed")
			}
			if err := transactional.Insert(ctx, &databasequeue.TaskRecord{Task: queue.Task{ID: "tx", MessageVersion: "test"}}); err != nil {
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
	if _, err := NewRepo(ctx, provider, func(context.Context, *databasequeue.TaskRecord) (*shadowTask, error) { return &shadowTask{}, nil }, Config{}); err == nil {
		t.Fatal("shadow accepted")
	}
	if _, err := NewRepo(ctx, provider, func(context.Context, *databasequeue.TaskRecord) (*prefixTask, error) { return &prefixTask{}, nil }, Config{}); err == nil {
		t.Fatal("prefix accepted")
	}
	if _, err := NewRepo(ctx, provider, func(context.Context, *databasequeue.TaskRecord) (*softTask, error) { return &softTask{}, nil }, Config{}); err == nil {
		t.Fatal("soft delete accepted")
	}
	if _, err := NewRepo(ctx, provider, func(context.Context, *databasequeue.TaskRecord) (*extraKeyTask, error) { return &extraKeyTask{}, nil }, Config{}); err == nil {
		t.Fatal("second key accepted")
	}
	if _, err := NewRepo[*testTask](ctx, provider, nil, Config{}); err == nil {
		t.Fatal("nil factory accepted")
	}
}

func TestRepoFactoryAndCanonicalID(t *testing.T) {
	repo, db := testRepo(t)
	ctx := context.Background()
	record := &databasequeue.TaskRecord{Task: queue.Task{ID: "id", MessageVersion: "test"}}
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
		r.Task.MessageVersion = "changed"
		return &testTask{}, nil
	}
	ids := []string{"id", "ID", "id ", "é", "e", "1", "01", "1e2", "100"}
	for _, id := range ids {
		record.Task.ID = id
		if err := repo.Insert(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := db.Model(&testTask{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != int64(len(ids)) {
		t.Fatal(count)
	}
	for _, id := range ids {
		var stored testTask
		if err := db.Where("id = ?", id).Take(&stored).Error; err != nil || string(stored.ID) != id {
			t.Fatalf("stored ID %q = %q, %v", id, stored.ID, err)
		}
	}
	got, err := repo.Claim(ctx, time.Now(), time.Now().Add(time.Hour), "t")
	if err != nil || got == nil || got.Task.MessageVersion != "test" {
		t.Fatalf("factory changed payload: %v %v", got, err)
	}
}

func TestRepoStoresOneTaskIDAndTimestampColumns(t *testing.T) {
	repo, db := testRepo(t)
	now := time.Unix(1700000000, 123456789).UTC()
	task := queue.Task{
		ID:             "Case-sensitive ID ",
		MessageVersion: "mail.v1",
		Payload:        []byte("body"),
		AvailableAt:    now,
		CreatedAt:      now.Add(-time.Minute),
	}
	if err := repo.Insert(context.Background(), &databasequeue.TaskRecord{Task: task}); err != nil {
		t.Fatal(err)
	}

	var row testTask
	if err := db.Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if string(row.ID) != task.ID || row.Generation == "" {
		t.Fatalf("stored identifiers = id %q generation %q", row.ID, row.Generation)
	}
	if !row.AvailableAt.Equal(time.Unix(1700000000, 124000000).UTC()) {
		t.Fatalf("available_at = %s", row.AvailableAt)
	}
	if row.FailedAt != nil || row.CompletedAt != nil {
		t.Fatalf("terminal timestamps = failed %v completed %v", row.FailedAt, row.CompletedAt)
	}
	var data map[string]any
	if err := json.Unmarshal(row.Data, &data); err != nil {
		t.Fatal(err)
	}
	if _, exists := data["id"]; exists {
		t.Fatalf("data duplicates task id: %s", row.Data)
	}
	if _, exists := data["available_at"]; exists {
		t.Fatalf("data duplicates available_at: %s", row.Data)
	}
}

func TestRepoValidatesMySQLTimestampRange(t *testing.T) {
	repo, _ := testRepo(t)
	repo.retainCompleted = true
	repo.timestampPrecision = time.Second
	repo.timestampMin = time.Date(1970, time.January, 1, 0, 0, 1, 0, time.UTC)
	repo.timestampMax = time.Date(2038, time.January, 19, 3, 14, 7, 0, time.UTC)
	for _, value := range []time.Time{repo.timestampMin, repo.timestampMax} {
		if err := repo.validateTimestamp("available_at", value); err != nil {
			t.Fatalf("valid timestamp %s rejected: %v", value, err)
		}
	}
	for _, value := range []time.Time{time.Time{}, repo.timestampMin.Add(-time.Second), repo.timestampMax.Add(time.Second)} {
		if err := repo.validateTimestamp("available_at", value); err == nil {
			t.Fatalf("invalid timestamp %s accepted", value)
		}
	}
	invalid := repo.timestampMax.Add(time.Second)
	operations := []func() error{
		func() error {
			return repo.Insert(context.Background(), &databasequeue.TaskRecord{Task: queue.Task{ID: "future", MessageVersion: "test", AvailableAt: invalid}})
		},
		func() error { return repo.CompleteReserved(context.Background(), "future", "token", invalid) },
		func() error { return repo.ReleaseReserved(context.Background(), "future", "token", invalid) },
		func() error { return repo.FailReserved(context.Background(), "future", "token", "test", invalid) },
		func() error { return repo.RetryTask(context.Background(), "future", invalid) },
	}
	for index, operation := range operations {
		if err := operation(); err == nil || !strings.Contains(err.Error(), "timestamp range") {
			t.Fatalf("operation %d error = %v", index, err)
		}
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
		retain   bool
	}{
		{name: "success", wantRuns: 1},
		{name: "success_retained", wantRuns: 1, retain: true},
		{name: "transient_then_success_retained", wantRuns: 2, retain: true},
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
			repo, err := NewRepo(context.Background(), repo.provider, repo.factory, Config{RetainCompleted: tc.retain})
			if err != nil {
				t.Fatal(err)
			}
			store := &integrationQueueStore{Store: databasequeue.NewStore(repo), settled: make(chan error, 1)}
			obs := integrationQueueObservability(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			original := &queue.Task{ID: "order-123", MessageVersion: "email.v1", Payload: []byte("invoice"), Headers: map[string]string{"tenant": "acme"}}
			version := 1
			if tc.name == "unknown_type" {
				version = 2
			}
			publisher, err := queue.NewQueue(queue.Definition[[]byte]{Queue: "email", Version: version, Codec: payloadCodec{}}, store, obs)
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
				case "transient_then_success", "transient_then_success_retained":
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
			runWorker := func(version int, handle func(context.Context, []byte) error) {
				t.Helper()
				q, err := queue.NewQueue(queue.Definition[[]byte]{Queue: "email", Version: version, Codec: payloadCodec{}}, store, obs)
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
			runWorker(1, handler)
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
				runWorker(version, func(context.Context, []byte) error { return nil })
			} else if len(failed) != 0 {
				t.Fatalf("successful task archived: %+v", failed)
			}
			var count int64
			wantCount := int64(0)
			if tc.retain {
				wantCount = 1
			}
			if err := db.Model(&testTask{}).Count(&count).Error; err != nil || count != wantCount {
				t.Fatalf("completed tasks count=%d want=%d error=%v", count, wantCount, err)
			}
			if tc.retain {
				var row testTask
				if err := db.First(&row).Error; err != nil {
					t.Fatal(err)
				}
				if row.Status != StatusCompleted || row.CompletedAt == nil || row.Attempts != tc.wantRuns {
					t.Fatalf("completion record: %+v", row.Model)
				}
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
