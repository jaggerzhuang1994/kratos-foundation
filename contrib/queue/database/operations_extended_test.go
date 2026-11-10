package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

// extendedRepoStub 记录到达仓储的调用；校验失败的请求不应到达这里。
type extendedRepoStub struct {
	operationsRepoStub
	calls  *int
	counts map[queue.TaskStatus]int64
	groups []queue.TaskGroup
	items  []queue.BulkItemResult
}

func (r extendedRepoStub) hit() { *r.calls++ }

func (r extendedRepoStub) CountTasks(context.Context, queue.TaskFilter) (int64, error) {
	r.hit()
	return 7, nil
}

func (r extendedRepoStub) CountTasksByStatus(context.Context, queue.TaskFilter) (map[queue.TaskStatus]int64, error) {
	r.hit()
	return r.counts, nil
}

func (r extendedRepoStub) GroupTasks(context.Context, queue.TaskFilter, queue.TaskGroupField, int) ([]queue.TaskGroup, error) {
	r.hit()
	return r.groups, nil
}

func (r extendedRepoStub) RescheduleTask(context.Context, string, time.Time) error {
	r.hit()
	return nil
}

func (r extendedRepoStub) bulk() (queue.BulkResult, error) {
	r.hit()
	return queue.BulkResult{Affected: 1, Items: r.items}, nil
}

func (r extendedRepoStub) RetryTasks(context.Context, []string, time.Time) (queue.BulkResult, error) {
	return r.bulk()
}

func (r extendedRepoStub) CancelTasks(context.Context, []string) (queue.BulkResult, error) {
	return r.bulk()
}

func (r extendedRepoStub) DeleteTasks(context.Context, []string) (queue.BulkResult, error) {
	return r.bulk()
}

func (r extendedRepoStub) RescheduleTasks(context.Context, []string, time.Time) (queue.BulkResult, error) {
	return r.bulk()
}

func (r extendedRepoStub) RetryMatching(context.Context, queue.TaskFilter, time.Time, int) (queue.BulkResult, error) {
	return r.bulk()
}

func (r extendedRepoStub) CancelMatching(context.Context, queue.TaskFilter, int) (queue.BulkResult, error) {
	return r.bulk()
}

func (r extendedRepoStub) DeleteMatching(context.Context, queue.TaskFilter, int) (queue.BulkResult, error) {
	return r.bulk()
}

func TestExtendedOperationsExposedOnlyForExtendedRepo(t *testing.T) {
	plain, _ := NewStore(operationsRepoStub{}).QueueOperations()
	if _, ok := plain.(queue.ExtendedOperations); ok {
		t.Fatal("plain OperationsRepo exposed extended operations")
	}
	calls := 0
	ops, ok := NewStore(extendedRepoStub{calls: &calls}).QueueOperations()
	if _, extended := ops.(queue.ExtendedOperations); !ok || !extended {
		t.Fatal("ExtendedOperationsRepo was not exposed as ExtendedOperations")
	}
}

func TestExtendedOperationsValidateBeforeReachingRepo(t *testing.T) {
	ctx := context.Background()
	calls := 0
	items := []queue.BulkItemResult{{ID: "a"}}
	repo := extendedRepoStub{calls: &calls, counts: map[queue.TaskStatus]int64{queue.TaskStatusFailed: 2}, groups: []queue.TaskGroup{{Value: "v1", Count: 3}}, items: items}
	operations, _ := NewStore(repo).QueueOperations()
	ext := operations.(queue.ExtendedOperations)
	bad := queue.TaskFilter{MinAttempts: -1}
	now := time.Now()

	rejected := []func() error{
		func() error { _, err := ext.Count(ctx, bad); return err },
		func() error { _, err := ext.CountByStatus(ctx, bad); return err },
		func() error {
			_, err := ext.CountByStatus(ctx, queue.TaskFilter{Statuses: []queue.TaskStatus{queue.TaskStatusFailed}})
			return err
		},
		func() error { _, err := ext.Group(ctx, bad, queue.TaskGroupFailureReason, 10); return err },
		func() error { _, err := ext.Group(ctx, queue.TaskFilter{}, "payload", 10); return err },
		func() error {
			_, err := ext.Group(ctx, queue.TaskFilter{}, queue.TaskGroupMessageVersion, 0)
			return err
		},
		func() error { return ext.Reschedule(ctx, "id", time.Time{}) },
		func() error { _, err := ext.RetryTasks(ctx, nil, now); return err },
		func() error { _, err := ext.CancelTasks(ctx, []string{" "}); return err },
		func() error { _, err := ext.DeleteTasks(ctx, make([]string, queue.MaxBulkTasks+1)); return err },
		func() error { _, err := ext.RescheduleTasks(ctx, []string{"a"}, time.Time{}); return err },
		func() error { _, err := ext.RetryMatching(ctx, bad, now, 10); return err },
		func() error { _, err := ext.CancelMatching(ctx, queue.TaskFilter{}, 0); return err },
		func() error { _, err := ext.DeleteMatching(ctx, queue.TaskFilter{}, queue.MaxBulkTasks+1); return err },
	}
	for index, call := range rejected {
		if err := call(); err == nil {
			t.Fatalf("case %d: invalid input accepted", index)
		}
	}
	if err := ext.Reschedule(ctx, " ", now); !errors.Is(err, queue.ErrNotFound) {
		t.Fatalf("Reschedule(blank id) = %v", err)
	}
	if calls != 0 {
		t.Fatalf("invalid input reached repo %d times", calls)
	}

	if count, err := ext.Count(ctx, queue.TaskFilter{}); err != nil || count != 7 {
		t.Fatalf("Count() = %d, %v", count, err)
	}
	counts, err := ext.CountByStatus(ctx, queue.TaskFilter{})
	if err != nil || len(counts) != 5 || counts[queue.TaskStatusFailed] != 2 || counts[queue.TaskStatusPending] != 0 {
		t.Fatalf("CountByStatus() = %v, %v", counts, err)
	}
	counts[queue.TaskStatusFailed] = 99
	if repo.counts[queue.TaskStatusFailed] != 2 {
		t.Fatal("CountByStatus() returned the repo map")
	}
	groups, err := ext.Group(ctx, queue.TaskFilter{}, queue.TaskGroupMessageVersion, 10)
	if err != nil || len(groups) != 1 {
		t.Fatalf("Group() = %v, %v", groups, err)
	}
	groups[0].Value = "changed"
	if repo.groups[0].Value != "v1" {
		t.Fatal("Group() returned the repo slice")
	}
	if err := ext.Reschedule(ctx, "a", now); err != nil {
		t.Fatal(err)
	}
	result, err := ext.RetryTasks(ctx, []string{"a"}, now)
	if err != nil || result.Affected != 1 || len(result.Items) != 1 {
		t.Fatalf("RetryTasks() = %#v, %v", result, err)
	}
	result.Items[0].ID = "changed"
	if items[0].ID != "a" {
		t.Fatal("RetryTasks() returned the repo items")
	}
	for _, call := range []func() (queue.BulkResult, error){
		func() (queue.BulkResult, error) { return ext.CancelTasks(ctx, []string{"a"}) },
		func() (queue.BulkResult, error) { return ext.DeleteTasks(ctx, []string{"a"}) },
		func() (queue.BulkResult, error) { return ext.RescheduleTasks(ctx, []string{"a"}, now) },
		func() (queue.BulkResult, error) { return ext.RetryMatching(ctx, queue.TaskFilter{}, now, 10) },
		func() (queue.BulkResult, error) { return ext.CancelMatching(ctx, queue.TaskFilter{}, 10) },
		func() (queue.BulkResult, error) { return ext.DeleteMatching(ctx, queue.TaskFilter{}, 10) },
	} {
		if result, err := call(); err != nil || result.Affected != 1 {
			t.Fatalf("bulk call = %#v, %v", result, err)
		}
	}
}
