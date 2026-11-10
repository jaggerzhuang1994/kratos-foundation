package gorm

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"gorm.io/gorm"
)

func TestCountAndGroupTasks(t *testing.T) {
	repo, db := testRepo(t)
	runCountAndGroupTasks(t, repo, db)
}

func runCountAndGroupTasks(t *testing.T, repo *Repo[*testTask], db *gorm.DB) {
	queryFixture(t, repo, db)
	ctx := context.Background()
	if count, err := repo.CountTasks(ctx, queue.TaskFilter{MessageVersions: []string{"v1"}}); err != nil || count != 3 {
		t.Fatalf("CountTasks(v1) = %d, %v", count, err)
	}
	if count, err := repo.CountTasks(ctx, queue.TaskFilter{}); err != nil || count != 6 {
		t.Fatalf("CountTasks() = %d, %v", count, err)
	}
	counts, err := repo.CountTasksByStatus(ctx, queue.TaskFilter{})
	want := map[queue.TaskStatus]int64{queue.TaskStatusPending: 2, queue.TaskStatusScheduled: 1, queue.TaskStatusRunning: 0, queue.TaskStatusFailed: 2, queue.TaskStatusCompleted: 1}
	if err != nil || !reflect.DeepEqual(counts, want) {
		t.Fatalf("CountTasksByStatus() = %v, %v", counts, err)
	}
	counts, err = repo.CountTasksByStatus(ctx, queue.TaskFilter{MessageVersions: []string{"v2"}})
	if err != nil || counts[queue.TaskStatusScheduled] != 1 || counts[queue.TaskStatusCompleted] != 1 || counts[queue.TaskStatusFailed] != 0 {
		t.Fatalf("CountTasksByStatus(v2) = %v, %v", counts, err)
	}
	if _, err := repo.CountTasksByStatus(ctx, queue.TaskFilter{Statuses: []queue.TaskStatus{queue.TaskStatusFailed}}); err == nil {
		t.Fatal("status filter accepted")
	}
	// 回填前 message_version 为 NULL 的旧行与空字符串归为一组。
	groups, err := repo.GroupTasks(ctx, queue.TaskFilter{}, queue.TaskGroupMessageVersion, 10)
	if err != nil || !reflect.DeepEqual(groups, []queue.TaskGroup{{Value: "v1", Count: 3}, {Value: "v2", Count: 2}, {Value: "", Count: 1}}) {
		t.Fatalf("GroupTasks(version) = %v, %v", groups, err)
	}
	groups, err = repo.GroupTasks(ctx, queue.TaskFilter{Statuses: []queue.TaskStatus{queue.TaskStatusFailed}}, queue.TaskGroupFailureReason, 1)
	if err != nil || !reflect.DeepEqual(groups, []queue.TaskGroup{{Value: "permanent", Count: 1}}) {
		t.Fatalf("GroupTasks(reason) = %v, %v", groups, err)
	}
	if _, err := repo.GroupTasks(ctx, queue.TaskFilter{}, "payload", 1); err == nil {
		t.Fatal("invalid group field accepted")
	}
}

func TestRescheduleAndBulkTasks(t *testing.T) {
	repo, db := testRepo(t)
	runRescheduleAndBulkTasks(t, repo, db, time.Millisecond)
}

func runRescheduleAndBulkTasks(t *testing.T, repo *Repo[*testTask], db *gorm.DB, precision time.Duration) {
	queryFixture(t, repo, db)
	ctx := context.Background()
	later := time.Now().UTC().Add(2 * time.Hour).Truncate(precision)
	// a 是 pending、d 是 scheduled：都可改排期；b 失败、c 完成：冲突；不存在：未找到。
	if err := repo.RescheduleTask(ctx, "a", later); err != nil {
		t.Fatal(err)
	}
	var row testTask
	if err := db.Where("id = ?", "a").Take(&row).Error; err != nil || !row.AvailableAt.Equal(later) || row.UpdatedAt == nil || row.Attempts != 0 {
		t.Fatalf("rescheduled row = %+v, %v", row.Model, err)
	}
	for id, want := range map[string]error{"b": queue.ErrStateConflict, "c": queue.ErrStateConflict, "missing": queue.ErrNotFound} {
		if err := repo.RescheduleTask(ctx, id, later); !errors.Is(err, want) {
			t.Fatalf("RescheduleTask(%s) = %v, want %v", id, err, want)
		}
	}
	// 有效租约的任务不能改排期：a 已排到两小时后，唯一到期的 f 被领取。
	claimed := "f"
	if record, err := repo.Claim(ctx, time.Now(), time.Now().Add(time.Hour), "owner"); err != nil || record == nil || record.Task.ID != claimed {
		t.Fatalf("claim = %+v, %v", record, err)
	}
	if err := repo.RescheduleTask(ctx, claimed, later); !errors.Is(err, queue.ErrStateConflict) {
		t.Fatalf("RescheduleTask(running) = %v", err)
	}

	result, err := repo.RescheduleTasks(ctx, []string{"d", "b", "missing", "d"}, time.Now().UTC())
	wantItems := []queue.BulkItemResult{{ID: "d"}, {ID: "b", Err: queue.ErrStateConflict}, {ID: "missing", Err: queue.ErrNotFound}, {ID: "d"}}
	if err != nil || result.Affected != 1 || !reflect.DeepEqual(result.Items, wantItems) {
		t.Fatalf("RescheduleTasks() = %+v, %v", result, err)
	}
	result, err = repo.RetryTasks(ctx, []string{"b", "c", "missing"}, time.Now().UTC())
	if err != nil || result.Affected != 1 || result.Items[0].Err != nil || !errors.Is(result.Items[1].Err, queue.ErrStateConflict) || !errors.Is(result.Items[2].Err, queue.ErrNotFound) {
		t.Fatalf("RetryTasks() = %+v, %v", result, err)
	}
	var retried testTask
	if err := db.Where("id = ?", "b").Take(&retried).Error; err != nil || retried.Status != StatusPending || retried.Attempts != 0 || retried.FailedAt != nil {
		t.Fatalf("retried row = %+v, %v", retried.Model, err)
	}
	result, err = repo.DeleteTasks(ctx, []string{"c", "a"})
	if err != nil || result.Affected != 1 || result.Items[0].Err != nil || !errors.Is(result.Items[1].Err, queue.ErrStateConflict) {
		t.Fatalf("DeleteTasks() = %+v, %v", result, err)
	}
	result, err = repo.CancelTasks(ctx, []string{"a", claimed})
	if err != nil || result.Affected != 1 || result.Items[0].Err != nil || !errors.Is(result.Items[1].Err, queue.ErrStateConflict) {
		t.Fatalf("CancelTasks() = %+v, %v", result, err)
	}
}

func TestMatchingOperations(t *testing.T) {
	repo, db := testRepo(t)
	runMatchingOperations(t, repo, db)
}

func runMatchingOperations(t *testing.T, repo *Repo[*testTask], db *gorm.DB) {
	queryFixture(t, repo, db)
	ctx := context.Background()
	// 失败任务 b、e：按 limit 分批，循环到 0 处理完全部匹配项。
	first, err := repo.RetryMatching(ctx, queue.TaskFilter{MessageVersions: []string{"v1"}}, time.Now().UTC(), 1)
	if err != nil || first.Affected != 1 {
		t.Fatalf("RetryMatching() first = %+v, %v", first, err)
	}
	second, err := repo.RetryMatching(ctx, queue.TaskFilter{MessageVersions: []string{"v1"}}, time.Now().UTC(), 1)
	if err != nil || second.Affected != 1 {
		t.Fatalf("RetryMatching() second = %+v, %v", second, err)
	}
	if done, err := repo.RetryMatching(ctx, queue.TaskFilter{}, time.Now().UTC(), 10); err != nil || done.Affected != 0 {
		t.Fatalf("RetryMatching() done = %+v, %v", done, err)
	}
	// 只删除终态：c 完成，其余都不是终态。
	if deleted, err := repo.DeleteMatching(ctx, queue.TaskFilter{}, 10); err != nil || deleted.Affected != 1 {
		t.Fatalf("DeleteMatching() = %+v, %v", deleted, err)
	}
	// 取消只针对没有有效租约的等待任务；筛选把范围限定在 v2（d）。
	if cancelled, err := repo.CancelMatching(ctx, queue.TaskFilter{MessageVersions: []string{"v2"}}, 10); err != nil || cancelled.Affected != 1 {
		t.Fatalf("CancelMatching() = %+v, %v", cancelled, err)
	}
	var remaining []string
	if err := db.Model(&testTask{}).Order("id").Pluck("id", &remaining).Error; err != nil || !reflect.DeepEqual(remaining, []string{"a", "b", "e", "f"}) {
		t.Fatalf("remaining = %v, %v", remaining, err)
	}
}
