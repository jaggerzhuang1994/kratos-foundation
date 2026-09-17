package gorm

import (
	"context"
	"errors"
	"testing"
	"time"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

func TestOperationsListPagesMetadataAndGetReturnsBody(t *testing.T) {
	repo, _ := testRepo(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, task := range []queue.Task{
		{ID: "pending", MessageVersion: "test", Payload: []byte("pending-body"), AvailableAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Hour)},
		{ID: "scheduled", MessageVersion: "test", Payload: []byte("scheduled-body"), AvailableAt: now.Add(time.Hour), CreatedAt: now},
	} {
		if err := repo.Insert(ctx, &databasequeue.TaskRecord{Task: task}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := repo.ListTasks(ctx, queue.TaskQuery{Limit: 1})
	if err != nil || len(first.Tasks) != 1 || first.NextCursor == "" {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	second, err := repo.ListTasks(ctx, queue.TaskQuery{Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(second.Tasks) != 1 || second.NextCursor != "" || second.Tasks[0].ID == first.Tasks[0].ID {
		t.Fatalf("second page = %#v, %v", second, err)
	}
	scheduled, err := repo.ListTasks(ctx, queue.TaskQuery{Limit: 10, Statuses: []queue.TaskStatus{queue.TaskStatusScheduled}})
	if err != nil || len(scheduled.Tasks) != 1 || scheduled.Tasks[0].ID != "scheduled" {
		t.Fatalf("scheduled page = %#v, %v", scheduled, err)
	}
	detail, err := repo.GetTask(ctx, "pending")
	if err != nil || detail.Task == nil || string(detail.Task.Payload) != "pending-body" || detail.Summary.Status != queue.TaskStatusPending {
		t.Fatalf("GetTask() = %#v, %v", detail, err)
	}
}

func TestOperationsEnforceStateAndBoundCleanup(t *testing.T) {
	repo, db := testRepo(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	repo, err := NewRepo(ctx, repo.provider, repo.factory, Config{RetainCompleted: true})
	if err != nil {
		t.Fatal(err)
	}
	insert := func(id string) {
		t.Helper()
		if err := repo.Insert(ctx, &databasequeue.TaskRecord{Task: queue.Task{ID: id, MessageVersion: "test", AvailableAt: now, CreatedAt: now}}); err != nil {
			t.Fatal(err)
		}
	}
	claim := func(id string) *databasequeue.TaskRecord {
		t.Helper()
		record, err := repo.Claim(ctx, now, now.Add(time.Hour), id+"-token")
		if err != nil || record == nil || record.Task.ID != id {
			t.Fatalf("Claim(%q) = %#v, %v", id, record, err)
		}
		return record
	}

	insert("running")
	running := claim("running")
	if err := repo.DeleteTask(ctx, "running"); !errors.Is(err, queue.ErrStateConflict) {
		t.Fatalf("DeleteTask(running) = %v", err)
	}
	if err := repo.CancelTask(ctx, "running"); !errors.Is(err, queue.ErrStateConflict) {
		t.Fatalf("CancelTask(running) = %v", err)
	}
	if err := repo.FailReserved(ctx, "running", running.Token, "test", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := repo.RetryTask(ctx, "running", now.Add(time.Hour)); err != nil {
		t.Fatalf("RetryTask(failed) = %v", err)
	}

	insert("completed")
	completed := claim("completed")
	if err := repo.CompleteReserved(ctx, "completed", completed.Token, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	insert("failed")
	failed := claim("failed")
	if err := repo.FailReserved(ctx, "failed", failed.Token, "test", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	result, err := repo.CleanupTasks(ctx, queue.CleanupOptions{Before: now, Limit: 1, Statuses: []queue.TaskStatus{queue.TaskStatusFailed, queue.TaskStatusCompleted}})
	if err != nil || result.Deleted != 1 {
		t.Fatalf("CleanupTasks() = %#v, %v", result, err)
	}
	var remaining int64
	if err := db.Model(&testTask{}).Where("status IN ?", []Status{StatusFailed, StatusCompleted}).Count(&remaining).Error; err != nil || remaining != 1 {
		t.Fatalf("terminal remaining = %d, %v", remaining, err)
	}
	if err := repo.DeleteTask(ctx, "missing"); !errors.Is(err, queue.ErrNotFound) {
		t.Fatalf("DeleteTask(missing) = %v", err)
	}
}

func TestCleanupTasksIncludesStoredTimestampStrictlyBeforeSubprecisionCutoff(t *testing.T) {
	repo, db := testRepo(t)
	ctx := context.Background()
	storedAt := time.Now().UTC().Truncate(repo.timestampPrecision)
	if err := repo.Insert(ctx, &databasequeue.TaskRecord{Task: queue.Task{ID: "failed", MessageVersion: "test", AvailableAt: storedAt, CreatedAt: storedAt}}); err != nil {
		t.Fatal(err)
	}
	record, err := repo.Claim(ctx, storedAt, storedAt.Add(time.Hour), "token")
	if err != nil || record == nil {
		t.Fatalf("Claim() = %#v, %v", record, err)
	}
	if err := repo.FailReserved(ctx, record.Task.ID, record.Token, "test", storedAt); err != nil {
		t.Fatal(err)
	}

	result, err := repo.CleanupTasks(ctx, queue.CleanupOptions{
		Before:   storedAt.Add(repo.timestampPrecision / 2),
		Limit:    1,
		Statuses: []queue.TaskStatus{queue.TaskStatusFailed},
	})
	if err != nil || result.Deleted != 1 {
		t.Fatalf("CleanupTasks() = %#v, %v", result, err)
	}
	var count int64
	if err := db.Model(&testTask{}).Where("id = ?", "failed").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed task count = %d, %v", count, err)
	}
}
