package gorm

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

func TestRepoLifecycle(t *testing.T) {
	for _, retain := range []bool{false, true} {
		t.Run(fmt.Sprintf("retain=%v", retain), func(t *testing.T) {
			repo, db := testRepo(t)
			repo, err := NewRepo(context.Background(), repo.provider, repo.factory, Config{RetainCompleted: retain})
			if err != nil {
				t.Fatal(err)
			}
			assertState := func(status Status, attempts int) {
				t.Helper()
				var row testTask
				if err := db.First(&row).Error; err != nil {
					t.Fatal(err)
				}
				if row.Status != status || row.Attempts != attempts || (row.Failed != (status == StatusFailed)) {
					t.Fatalf("state: %+v", row.Model)
				}
				if status != StatusRunning && (row.Token != "" || row.ReservedUntil != 0) {
					t.Fatalf("terminal or pending task has lease: %+v", row.Model)
				}
			}
			var _ databasequeue.Repo = repo
			store := databasequeue.NewStore(repo)
			ctx := context.Background()
			now := time.Unix(1700000000, 123456)
			task := &queue.Task{ID: "任务A ", Type: "email", Payload: []byte{0, 255}, Headers: map[string]string{"k": "v"}, AvailableAt: now}
			if err := store.Enqueue(ctx, task); err != nil {
				t.Fatal(err)
			}
			assertState(StatusPending, 0)
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
			assertState(StatusRunning, 1)
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
			assertState(StatusPending, 2)
			third, err := store.Reserve(ctx, now.Add(6*time.Second), time.Second)
			if err != nil || third == nil || third.Attempts != 3 {
				t.Fatalf("release: %v %v", third, err)
			}
			if err := store.Fail(ctx, third, "test", now); err != nil {
				t.Fatal(err)
			}
			assertState(StatusFailed, 3)
			failed, err := store.Failed(ctx, 10)
			if err != nil || len(failed) != 1 || failed[0].Reason != "test" {
				t.Fatalf("failed: %v %v", failed, err)
			}
			if err := store.Retry(ctx, task.ID, now); err != nil {
				t.Fatal(err)
			}
			assertState(StatusPending, 0)
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
			if retain {
				assertState(StatusCompleted, 1)
				if err := db.First(&saved).Error; err != nil {
					t.Fatal(err)
				}
				if saved.CompletedAt <= 0 || saved.TenantID != "tenant" || saved.OrderID != task.ID {
					t.Fatalf("lost completion details: %+v", saved)
				}
				record, err := saved.record()
				if err != nil || string(record.Task.Payload) != string(task.Payload) || record.Task.Headers["k"] != "v" {
					t.Fatalf("lost payload: %+v %v", record, err)
				}
				if err := store.Enqueue(ctx, task); !errors.Is(err, queue.ErrDuplicate) {
					t.Fatalf("reused retained id: %v", err)
				}
				// 重启并关闭保留开关也不能重新领取或删除已有完成记录。
				restarted, err := NewRepo(ctx, repo.provider, repo.factory, Config{})
				if err != nil {
					t.Fatal(err)
				}
				if got, err := restarted.Claim(ctx, now.Add(time.Hour), now.Add(2*time.Hour), "restart"); err != nil || got != nil {
					t.Fatalf("completed task reclaimed: %+v %v", got, err)
				}
				for _, op := range []func() error{
					func() error { return store.Ack(ctx, last) },
					func() error { return store.Release(ctx, last, now) },
					func() error { return store.Fail(ctx, last, "late", now) },
				} {
					if err := op(); !errors.Is(err, queue.ErrLeaseLost) {
						t.Fatalf("completed task changed: %v", err)
					}
				}
				assertState(StatusCompleted, 1)
				return
			}
			if err := store.Enqueue(ctx, task); err != nil {
				t.Fatal(err)
			}
			if err := store.Ack(ctx, last); !errors.Is(err, queue.ErrLeaseLost) {
				t.Fatal("old owner deleted new task")
			}
		})
	}
}

func TestCompleteReservedRejectsDuplicateAndFailedWrites(t *testing.T) {
	repo, db := testRepo(t)
	ctx, cancelTimeout := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelTimeout()
	repo, err := NewRepo(ctx, repo.provider, repo.factory, Config{RetainCompleted: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0).UTC()
	if err := repo.Insert(ctx, &databasequeue.TaskRecord{Task: queue.Task{ID: "complete", Type: "job", AvailableAt: now}}); err != nil {
		t.Fatal(err)
	}
	record, err := repo.Claim(ctx, now, now.Add(time.Minute), "owner")
	if err != nil || record == nil {
		t.Fatalf("claim: %v %v", record, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := repo.CompleteReserved(cancelled, "complete", "owner", now); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled completion: %v", err)
	}
	var row testTask
	if err := db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != StatusRunning || row.CompletedAt != 0 {
		t.Fatalf("failed update changed state: %+v", row.Model)
	}
	results := make(chan error, 2)
	// 两次确认竞争同一 token；数据库条件更新必须只允许一个成功，所有 goroutine 由下方接收收敛。
	for range 2 {
		go func() { results <- repo.CompleteReserved(ctx, "complete", "owner", now) }()
	}
	success, lost := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, queue.ErrLeaseLost) {
			lost++
		} else {
			t.Error(err)
		}
	}
	if success != 1 || lost != 1 {
		t.Fatalf("completion results: success=%d lost=%d", success, lost)
	}
	if err := db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != StatusCompleted || row.CompletedAt != now.UnixMilli() {
		t.Fatalf("completion time: %+v", row.Model)
	}
}
