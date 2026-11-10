package gorm

import (
	"context"
	"errors"
	"fmt"
	"time"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"gorm.io/gorm"
)

// 各运维变更允许的实际状态；条件与单条操作一致，在同一条 SQL 里原子校验。
const (
	retryCondition    = "status = 'failed' AND failed = ?"
	deleteCondition   = "status IN ('failed', 'completed')"
	waitingCondition  = "status NOT IN ('failed', 'completed') AND (reserved_until = 0 OR reserved_until <= ?)"
	groupValueColumn  = "group_value"
	groupCountColumn  = "group_count"
	statusCountPrefix = "count_"
)

// CountTasks 返回匹配筛选条件的任务数。
func (r *Repo[T]) CountTasks(ctx context.Context, filter queue.TaskFilter) (int64, error) {
	var count int64
	if err := r.applyFilter(r.consumerDB(ctx), filter, time.Now().UTC()).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count queue tasks: %w", err)
	}
	return count, nil
}

// CountTasksByStatus 用一条聚合查询返回各状态数量；状态判定与 ListTasks 相同。
func (r *Repo[T]) CountTasksByStatus(ctx context.Context, filter queue.TaskFilter) (map[queue.TaskStatus]int64, error) {
	if len(filter.Statuses) > 0 {
		return nil, errors.New("queue count by status does not accept a status filter")
	}
	now := time.Now().UTC()
	statuses := []queue.TaskStatus{queue.TaskStatusPending, queue.TaskStatusScheduled, queue.TaskStatusRunning, queue.TaskStatusFailed, queue.TaskStatusCompleted}
	query := ""
	args := make([]any, 0, len(statuses)*3)
	for index, status := range statuses {
		condition, values := statusCondition(status, now, r.timestampPrecision)
		if index > 0 {
			query += ", "
		}
		query += "COALESCE(SUM(CASE WHEN " + condition + " THEN 1 ELSE 0 END), 0) AS " + statusCountPrefix + string(status)
		args = append(args, values...)
	}
	row := map[string]any{}
	if err := r.applyFilter(r.consumerDB(ctx), filter, now).Select(query, args...).Take(&row).Error; err != nil {
		return nil, fmt.Errorf("count queue tasks by status: %w", err)
	}
	result := make(map[queue.TaskStatus]int64, len(statuses))
	for _, status := range statuses {
		count, err := toInt64(row[statusCountPrefix+string(status)])
		if err != nil {
			return nil, fmt.Errorf("count queue tasks by status: %w", err)
		}
		result[status] = count
	}
	return result, nil
}

// GroupTasks 按消息版本或失败分类分组计数；NULL（回填前的旧行）与空字符串合为一组。
func (r *Repo[T]) GroupTasks(ctx context.Context, filter queue.TaskFilter, field queue.TaskGroupField, limit int) ([]queue.TaskGroup, error) {
	var column string
	switch field {
	case queue.TaskGroupMessageVersion:
		column = "message_version"
	case queue.TaskGroupFailureReason:
		column = "failure_reason"
	default:
		return nil, errors.New("queue group field is invalid")
	}
	expression := "COALESCE(" + column + ", '')"
	var rows []map[string]any
	err := r.applyFilter(r.consumerDB(ctx), filter, time.Now().UTC()).
		Select(expression + " AS " + groupValueColumn + ", COUNT(*) AS " + groupCountColumn).
		Group(expression).Order(groupCountColumn + " DESC, " + groupValueColumn + " ASC").Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("group queue tasks: %w", err)
	}
	groups := make([]queue.TaskGroup, 0, len(rows))
	for _, row := range rows {
		count, err := toInt64(row[groupCountColumn])
		if err != nil {
			return nil, fmt.Errorf("group queue tasks: %w", err)
		}
		groups = append(groups, queue.TaskGroup{Value: toString(row[groupValueColumn]), Count: count})
	}
	return groups, nil
}

// RescheduleTask 修改没有有效租约的 pending/scheduled 任务的执行时间，清除过期租约，不改变尝试次数。
func (r *Repo[T]) RescheduleTask(ctx context.Context, id string, at time.Time) error {
	if err := r.validateTimestamp("available_at", at); err != nil {
		return err
	}
	result := r.consumerDB(ctx).Where("id = ?", id).Where(waitingCondition, time.Now().UnixMilli()).Updates(r.rescheduleValues(at))
	return r.operationsTransitionResult(ctx, result, id, "reschedule")
}

func (r *Repo[T]) rescheduleValues(at time.Time) map[string]any {
	return r.touch(map[string]any{"status": StatusPending, "available_at": timestampCeil(at, r.timestampPrecision), "reserved_until": 0, "token": ""})
}

// RetryTasks 逐个重试 failed 任务。
func (r *Repo[T]) RetryTasks(ctx context.Context, ids []string, at time.Time) (queue.BulkResult, error) {
	if err := r.validateTimestamp("available_at", at); err != nil {
		return queue.BulkResult{}, err
	}
	return r.bulkByID(ctx, ids, "retry", func(db *gorm.DB) *gorm.DB {
		return db.Where(retryCondition, true).Updates(r.retryValues(at))
	})
}

// CancelTasks 逐个删除没有有效租约的 pending/scheduled 任务。
func (r *Repo[T]) CancelTasks(ctx context.Context, ids []string) (queue.BulkResult, error) {
	return r.bulkByID(ctx, ids, "cancel", func(db *gorm.DB) *gorm.DB {
		return db.Where(waitingCondition, time.Now().UnixMilli()).Delete(r.entity())
	})
}

// DeleteTasks 逐个删除 failed/completed 任务。
func (r *Repo[T]) DeleteTasks(ctx context.Context, ids []string) (queue.BulkResult, error) {
	return r.bulkByID(ctx, ids, "delete", func(db *gorm.DB) *gorm.DB {
		return db.Where(deleteCondition).Delete(r.entity())
	})
}

// RescheduleTasks 逐个修改没有有效租约的 pending/scheduled 任务的执行时间。
func (r *Repo[T]) RescheduleTasks(ctx context.Context, ids []string, at time.Time) (queue.BulkResult, error) {
	if err := r.validateTimestamp("available_at", at); err != nil {
		return queue.BulkResult{}, err
	}
	return r.bulkByID(ctx, ids, "reschedule", func(db *gorm.DB) *gorm.DB {
		return db.Where(waitingCondition, time.Now().UnixMilli()).Updates(r.rescheduleValues(at))
	})
}

// RetryMatching 重试匹配条件的 failed 任务，至多 limit 条。
func (r *Repo[T]) RetryMatching(ctx context.Context, filter queue.TaskFilter, at time.Time, limit int) (queue.BulkResult, error) {
	if err := r.validateTimestamp("available_at", at); err != nil {
		return queue.BulkResult{}, err
	}
	return r.matching(ctx, filter, limit, "retry", retryCondition, []any{true}, func(db *gorm.DB) *gorm.DB {
		return db.Updates(r.retryValues(at))
	})
}

// CancelMatching 删除匹配条件、没有有效租约的 pending/scheduled 任务，至多 limit 条。
func (r *Repo[T]) CancelMatching(ctx context.Context, filter queue.TaskFilter, limit int) (queue.BulkResult, error) {
	return r.matching(ctx, filter, limit, "cancel", waitingCondition, []any{time.Now().UnixMilli()}, func(db *gorm.DB) *gorm.DB {
		return db.Delete(r.entity())
	})
}

// DeleteMatching 删除匹配条件的 failed/completed 任务，至多 limit 条。
func (r *Repo[T]) DeleteMatching(ctx context.Context, filter queue.TaskFilter, limit int) (queue.BulkResult, error) {
	return r.matching(ctx, filter, limit, "delete", deleteCondition, nil, func(db *gorm.DB) *gorm.DB {
		return db.Delete(r.entity())
	})
}

// bulkByID 对每个 ID 单独执行一条条件语句（不包外层事务，不长时间持有行锁），
// 再按变更前是否存在区分 ErrNotFound 与 ErrStateConflict。重复 ID 只执行一次，结果按请求顺序返回。
func (r *Repo[T]) bulkByID(ctx context.Context, ids []string, operation string, apply func(*gorm.DB) *gorm.DB) (queue.BulkResult, error) {
	var found []string
	if err := r.consumerDB(ctx).Where("id IN ?", ids).Pluck("id", &found).Error; err != nil {
		return queue.BulkResult{}, fmt.Errorf("%s queue tasks: %w", operation, err)
	}
	existing := make(map[string]bool, len(found))
	for _, id := range found {
		existing[id] = true
	}
	outcome := make(map[string]error, len(ids))
	result := queue.BulkResult{Items: make([]queue.BulkItemResult, 0, len(ids))}
	for _, id := range ids {
		err, done := outcome[id]
		if !done {
			switch {
			case !existing[id]:
				err = queue.ErrNotFound
			default:
				changed := apply(r.consumerDB(ctx).Where("id = ?", id))
				if changed.Error != nil {
					return result, fmt.Errorf("%s queue task: %w", operation, changed.Error)
				}
				if changed.RowsAffected == 0 {
					err = queue.ErrStateConflict
				} else {
					result.Affected++
				}
			}
			outcome[id] = err
		}
		result.Items = append(result.Items, queue.BulkItemResult{ID: id, Err: err})
	}
	return result, nil
}

// matching 先按筛选与状态条件取至多 limit 个 ID，再带同样的状态条件变更；
// 两步之间状态已变化的任务不会被误改，Affected 以实际变更行数为准。
func (r *Repo[T]) matching(ctx context.Context, filter queue.TaskFilter, limit int, operation, condition string, args []any, mutate func(*gorm.DB) *gorm.DB) (queue.BulkResult, error) {
	var ids []string
	query := r.applyFilter(r.consumerDB(ctx), filter, time.Now().UTC()).Where(condition, args...)
	if err := query.Order("id ASC").Limit(limit).Pluck("id", &ids).Error; err != nil {
		return queue.BulkResult{}, fmt.Errorf("select %s queue tasks: %w", operation, err)
	}
	if len(ids) == 0 {
		return queue.BulkResult{}, nil
	}
	changed := mutate(r.consumerDB(ctx).Where("id IN ?", ids).Where(condition, args...))
	if changed.Error != nil {
		return queue.BulkResult{}, fmt.Errorf("%s queue tasks: %w", operation, changed.Error)
	}
	return queue.BulkResult{Affected: int(changed.RowsAffected)}, nil
}

// toInt64 兼容不同驱动对聚合结果返回的整数、浮点、字节串类型。
func toInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	case int32:
		return int64(typed), nil
	case uint64:
		return int64(typed), nil
	case float64:
		return int64(typed), nil
	case []byte:
		var n int64
		_, err := fmt.Sscan(string(typed), &n)
		return n, err
	case string:
		var n int64
		_, err := fmt.Sscan(typed, &n)
		return n, err
	case nil:
		return 0, nil
	default:
		return 0, fmt.Errorf("unexpected aggregate type %T", value)
	}
}

func toString(value any) string {
	switch typed := value.(type) {
	case []byte:
		return string(typed)
	case string:
		return typed
	default:
		return ""
	}
}

var _ databasequeue.ExtendedOperationsRepo = (*Repo[*simpleModel])(nil)
