package gorm

import (
	"context"
	"errors"
	"testing"
	"time"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

func TestRepoRecordsLastExecutionError(t *testing.T) {
	repo, db := testRepo(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := repo.Insert(ctx, &databasequeue.TaskRecord{Task: queue.Task{ID: "a", MessageVersion: "v", AvailableAt: now}}); err != nil {
		t.Fatal(err)
	}
	var row testTask
	if err := db.First(&row).Error; err != nil || row.LastErrorMessage != nil || row.LastErrorAt != nil {
		t.Fatalf("new task has error columns: %+v %v", row.Model, err)
	}
	store := databasequeue.NewStore(repo)
	claim := func(at time.Time) *queue.Reservation {
		t.Helper()
		reservation, err := store.Reserve(ctx, at, time.Second)
		if err != nil || reservation == nil {
			t.Fatalf("claim: %v %v", reservation, err)
		}
		return reservation
	}
	assertSummary := func(message string, at time.Time) {
		t.Helper()
		snapshot, err := repo.GetTask(ctx, "a")
		if err != nil || snapshot.Summary.LastError != message || !snapshot.Summary.LastErrorAt.Equal(at) {
			t.Fatalf("summary %+v err=%v", snapshot.Summary, err)
		}
		page, err := repo.ListTasks(ctx, queue.TaskQuery{Limit: 10})
		if err != nil || len(page.Tasks) != 1 || page.Tasks[0].LastError != message {
			t.Fatalf("page %+v err=%v", page, err)
		}
	}

	first := claim(now)
	firstErr := queue.ExecutionError{Message: "first failure", At: now.Add(time.Millisecond)}
	if err := store.ReleaseWithError(ctx, first, now.Add(time.Second), firstErr); err != nil {
		t.Fatal(err)
	}
	assertSummary("first failure", firstErr.At)
	if err := store.ReleaseWithError(ctx, first, now, firstErr); !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatalf("stale release = %v", err)
	}

	second := claim(now.Add(time.Second))
	secondErr := queue.ExecutionError{Message: "second failure", At: now.Add(2 * time.Second)}
	if err := store.FailWithError(ctx, second, "retry_exhausted", secondErr.At, secondErr); err != nil {
		t.Fatal(err)
	}
	assertSummary("second failure", secondErr.At)
	failed, err := store.Failed(ctx, 10)
	if err != nil || len(failed) != 1 || failed[0].LastError != "second failure" || failed[0].Reason != "retry_exhausted" {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}

	// 人工重试与成功确认不清除最近一次错误，便于排查曾经失败过的任务。
	if err := repo.RetryTask(ctx, "a", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	assertSummary("second failure", secondErr.At)
	retainRepo, err := NewRepo(ctx, repo.provider, repo.factory, Config{RetainCompleted: true})
	if err != nil {
		t.Fatal(err)
	}
	third, err := databasequeue.NewStore(retainRepo).Reserve(ctx, now.Add(3*time.Second), time.Second)
	if err != nil || third == nil {
		t.Fatalf("claim after retry: %v %v", third, err)
	}
	if err := retainRepo.CompleteReserved(ctx, "a", third.Token, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	assertSummary("second failure", secondErr.At)
}

func TestRepoReadsRowsWithoutErrorColumnsValues(t *testing.T) {
	repo, db := testRepo(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := repo.Insert(ctx, &databasequeue.TaskRecord{Task: queue.Task{ID: "old", MessageVersion: "v", AvailableAt: now}}); err != nil {
		t.Fatal(err)
	}
	// 迁移前的存量行两列都是 NULL；读取与普通释放、失败均不受影响。
	if err := db.Exec("UPDATE queue_test_tasks SET last_error_message = NULL, last_error_at = NULL").Error; err != nil {
		t.Fatal(err)
	}
	store := databasequeue.NewStore(repo)
	reservation, err := store.Reserve(ctx, now, time.Second)
	if err != nil || reservation == nil {
		t.Fatalf("claim: %v %v", reservation, err)
	}
	if err := store.Fail(ctx, reservation, "permanent", now); err != nil {
		t.Fatal(err)
	}
	failed, err := store.Failed(ctx, 10)
	if err != nil || len(failed) != 1 || failed[0].LastError != "" || !failed[0].LastErrorAt.IsZero() {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
}

func TestRepoValidatesLastErrorTimestamp(t *testing.T) {
	repo, _ := testRepo(t)
	repo.timestampMin = time.Date(1970, time.January, 1, 0, 0, 1, 0, time.UTC)
	repo.timestampMax = time.Date(2038, time.January, 19, 3, 14, 7, 0, time.UTC)
	ctx := context.Background()
	now := time.Now().UTC()
	outside := queue.ExecutionError{Message: "boom", At: time.Date(2040, time.January, 1, 0, 0, 0, 0, time.UTC)}
	if err := repo.ReleaseReservedWithError(ctx, "a", "t", now, outside); err == nil {
		t.Fatal("release accepted an out-of-range error time")
	}
	if err := repo.FailReservedWithError(ctx, "a", "t", "permanent", now, outside); err == nil {
		t.Fatal("fail accepted an out-of-range error time")
	}
}
