package database

import (
	"context"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

// ExtendedOperationsRepo 是 OperationsRepo 的可选扩展：筛选计数、分组、改排期与批量变更。
// ListTasks 实现该接口时还必须支持 TaskQuery.Filter 与 TaskQuery.Sort。
// 每个变更必须在存储层原子校验实际状态与租约；按条件批量操作不得超过 limit 条。
type ExtendedOperationsRepo interface {
	OperationsRepo
	CountTasks(context.Context, queue.TaskFilter) (int64, error)
	CountTasksByStatus(context.Context, queue.TaskFilter) (map[queue.TaskStatus]int64, error)
	GroupTasks(ctx context.Context, filter queue.TaskFilter, field queue.TaskGroupField, limit int) ([]queue.TaskGroup, error)
	RescheduleTask(ctx context.Context, id string, at time.Time) error
	RetryTasks(ctx context.Context, ids []string, at time.Time) (queue.BulkResult, error)
	CancelTasks(ctx context.Context, ids []string) (queue.BulkResult, error)
	DeleteTasks(ctx context.Context, ids []string) (queue.BulkResult, error)
	RescheduleTasks(ctx context.Context, ids []string, at time.Time) (queue.BulkResult, error)
	RetryMatching(ctx context.Context, filter queue.TaskFilter, at time.Time, limit int) (queue.BulkResult, error)
	CancelMatching(ctx context.Context, filter queue.TaskFilter, limit int) (queue.BulkResult, error)
	DeleteMatching(ctx context.Context, filter queue.TaskFilter, limit int) (queue.BulkResult, error)
}

// extendedOperations 在基础运维能力之上统一校验扩展操作的输入，返回独立副本。
type extendedOperations struct {
	operations
	// repo 是同一底层仓储的扩展视图。
	repo ExtendedOperationsRepo
}

var _ queue.ExtendedOperations = (*extendedOperations)(nil)

func (o *extendedOperations) Count(ctx context.Context, filter queue.TaskFilter) (int64, error) {
	if err := filter.Validate(); err != nil {
		return 0, err
	}
	return o.repo.CountTasks(ctx, filter)
}

func (o *extendedOperations) CountByStatus(ctx context.Context, filter queue.TaskFilter) (map[queue.TaskStatus]int64, error) {
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	if len(filter.Statuses) > 0 {
		return nil, errors.New("queue count by status does not accept a status filter")
	}
	counts, err := o.repo.CountTasksByStatus(ctx, filter)
	if err != nil {
		return nil, err
	}
	result := maps.Clone(counts)
	if result == nil {
		result = make(map[queue.TaskStatus]int64, 5)
	}
	// 未出现的状态补 0，调用方可直接按全部状态渲染。
	for _, status := range []queue.TaskStatus{queue.TaskStatusPending, queue.TaskStatusScheduled, queue.TaskStatusRunning, queue.TaskStatusFailed, queue.TaskStatusCompleted} {
		if _, ok := result[status]; !ok {
			result[status] = 0
		}
	}
	return result, nil
}

func (o *extendedOperations) Group(ctx context.Context, filter queue.TaskFilter, field queue.TaskGroupField, limit int) ([]queue.TaskGroup, error) {
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	if field != queue.TaskGroupMessageVersion && field != queue.TaskGroupFailureReason {
		return nil, errors.New("queue group field is invalid")
	}
	if limit < 1 || limit > 1000 {
		return nil, errors.New("queue group limit must be between 1 and 1000")
	}
	groups, err := o.repo.GroupTasks(ctx, filter, field, limit)
	return slices.Clone(groups), err
}

func (o *extendedOperations) Reschedule(ctx context.Context, id string, at time.Time) error {
	if !validOperationsID(id) {
		return queue.ErrNotFound
	}
	if at.IsZero() {
		return errors.New("queue reschedule time is required")
	}
	return o.repo.RescheduleTask(ctx, id, at)
}

func (o *extendedOperations) RetryTasks(ctx context.Context, ids []string, at time.Time) (queue.BulkResult, error) {
	return bulk(ids, func() (queue.BulkResult, error) { return o.repo.RetryTasks(ctx, ids, at) })
}

func (o *extendedOperations) CancelTasks(ctx context.Context, ids []string) (queue.BulkResult, error) {
	return bulk(ids, func() (queue.BulkResult, error) { return o.repo.CancelTasks(ctx, ids) })
}

func (o *extendedOperations) DeleteTasks(ctx context.Context, ids []string) (queue.BulkResult, error) {
	return bulk(ids, func() (queue.BulkResult, error) { return o.repo.DeleteTasks(ctx, ids) })
}

func (o *extendedOperations) RescheduleTasks(ctx context.Context, ids []string, at time.Time) (queue.BulkResult, error) {
	if at.IsZero() {
		return queue.BulkResult{}, errors.New("queue reschedule time is required")
	}
	return bulk(ids, func() (queue.BulkResult, error) { return o.repo.RescheduleTasks(ctx, ids, at) })
}

func (o *extendedOperations) RetryMatching(ctx context.Context, filter queue.TaskFilter, at time.Time, limit int) (queue.BulkResult, error) {
	return matching(filter, limit, func() (queue.BulkResult, error) { return o.repo.RetryMatching(ctx, filter, at, limit) })
}

func (o *extendedOperations) CancelMatching(ctx context.Context, filter queue.TaskFilter, limit int) (queue.BulkResult, error) {
	return matching(filter, limit, func() (queue.BulkResult, error) { return o.repo.CancelMatching(ctx, filter, limit) })
}

func (o *extendedOperations) DeleteMatching(ctx context.Context, filter queue.TaskFilter, limit int) (queue.BulkResult, error) {
	return matching(filter, limit, func() (queue.BulkResult, error) { return o.repo.DeleteMatching(ctx, filter, limit) })
}

func bulk(ids []string, run func() (queue.BulkResult, error)) (queue.BulkResult, error) {
	if err := queue.ValidateBulkIDs(ids); err != nil {
		return queue.BulkResult{}, err
	}
	result, err := run()
	result.Items = slices.Clone(result.Items)
	return result, err
}

func matching(filter queue.TaskFilter, limit int, run func() (queue.BulkResult, error)) (queue.BulkResult, error) {
	if err := filter.Validate(); err != nil {
		return queue.BulkResult{}, err
	}
	if err := queue.ValidateBulkLimit(limit); err != nil {
		return queue.BulkResult{}, err
	}
	return run()
}
