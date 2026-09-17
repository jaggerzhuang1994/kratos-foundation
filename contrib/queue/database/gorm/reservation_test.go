package gorm

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"gorm.io/gorm"
)

func TestRepoConcurrentClaim(t *testing.T) {
	repo, _ := testRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := repo.Insert(ctx, &databasequeue.TaskRecord{Task: queue.Task{ID: "one", MessageVersion: "test", AvailableAt: now}}); err != nil {
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

func TestRepoCorruptPayload(t *testing.T) {
	repo, db := testRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := repo.Insert(ctx, &databasequeue.TaskRecord{Task: queue.Task{ID: "bad", MessageVersion: "test"}}); err != nil {
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
	if string(stored.Data) != "broken" || !stored.Failed || stored.Status != StatusFailed {
		t.Fatal("corrupt data discarded")
	}
}

func TestRepoClaimRejectsRecreatedTask(t *testing.T) {
	repo, db := testRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	record := &databasequeue.TaskRecord{Task: queue.Task{ID: "same", MessageVersion: "test"}}
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

func TestRepoClaimOnlyExecutableStates(t *testing.T) {
	repo, db := testRepo(t)
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	// 终态和未知状态即使没有 failed 标记，也不能占用领取候选；有效租约和未来排期仍须等待。
	fixtures := []struct {
		id                  string
		status              Status
		available, reserved time.Time
	}{
		{"completed", StatusCompleted, now.Add(-time.Hour), time.Time{}},
		{"failed", StatusFailed, now.Add(-time.Hour), time.Time{}},
		{"unknown", Status("unknown"), now.Add(-time.Hour), time.Time{}},
		{"leased", StatusRunning, now.Add(-time.Hour), now.Add(time.Hour)},
		{"future", StatusPending, now.Add(time.Hour), time.Time{}},
		{"expired", StatusRunning, now.Add(-time.Minute), now},
		{"pending", StatusPending, now, time.Time{}},
	}
	for _, f := range fixtures {
		if err := repo.Insert(ctx, &databasequeue.TaskRecord{Task: queue.Task{ID: f.id, MessageVersion: "test", AvailableAt: f.available}}); err != nil {
			t.Fatal(err)
		}
		var reserved int64
		if !f.reserved.IsZero() {
			reserved = f.reserved.UnixMilli()
		}
		if err := db.Model(&testTask{}).Where("id = ?", taskKey(f.id)).Updates(map[string]any{"status": f.status, "reserved_until": reserved}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"expired", "pending"} {
		record, err := repo.Claim(ctx, now, now.Add(time.Hour), "owner-"+id)
		if err != nil || record == nil || record.Task.ID != id {
			t.Fatalf("want %s, got %+v: %v", id, record, err)
		}
	}
	if record, err := repo.Claim(ctx, now, now.Add(time.Hour), "empty"); err != nil || record != nil {
		t.Fatalf("unexpected candidate: %+v %v", record, err)
	}
}
