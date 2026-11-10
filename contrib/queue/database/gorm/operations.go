package gorm

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ListTasks 按筛选条件和排序返回一页任务元数据；正文仅由 GetTask 返回。
// 默认排序按存储主键升序，游标与旧版本兼容；其他排序使用绑定排序方式的键集游标。
func (r *Repo[T]) ListTasks(ctx context.Context, query queue.TaskQuery) (queue.TaskPage, error) {
	if err := query.Validate(); err != nil {
		return queue.TaskPage{}, err
	}
	now := time.Now().UTC()
	db := r.applyFilter(r.consumerDB(ctx), query.EffectiveFilter(), now)
	if query.Sort.Field == queue.TaskSortDefault {
		if query.Cursor != "" {
			cursor, err := decodeOperationsCursor(query.Cursor)
			if err != nil {
				return queue.TaskPage{}, err
			}
			db = db.Where("id > ?", cursor)
		}
		db = db.Order("id ASC")
	} else {
		var cursor *listCursor
		if query.Cursor != "" {
			decoded, err := decodeListCursor(query.Cursor, query.Sort)
			if err != nil {
				return queue.TaskPage{}, err
			}
			cursor = &decoded
		}
		db = applySort(db, query.Sort, cursor)
	}
	var entities []T
	if err := db.Limit(query.Limit + 1).Find(&entities).Error; err != nil {
		return queue.TaskPage{}, fmt.Errorf("list queue tasks: %w", err)
	}
	page := queue.TaskPage{Tasks: make([]queue.TaskSummary, 0, min(len(entities), query.Limit))}
	for index, entity := range entities {
		if index == query.Limit {
			last := entities[index-1].QueueModel()
			if query.Sort.Field == queue.TaskSortDefault {
				page.NextCursor = encodeOperationsCursor(string(last.ID))
			} else {
				page.NextCursor = encodeListCursor(cursorAfter(last, query.Sort))
			}
			break
		}
		summary, err := entity.QueueModel().summary(now, r.timestampPrecision)
		if err != nil {
			return queue.TaskPage{}, err
		}
		page.Tasks = append(page.Tasks, summary)
	}
	return page, nil
}

// GetTask 按逐字节任务 ID 返回包含完整正文的独立快照。
func (r *Repo[T]) GetTask(ctx context.Context, id string) (queue.TaskSnapshot, error) {
	entity := r.entity()
	result := r.consumerDB(ctx).Where("id = ?", id).First(entity)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return queue.TaskSnapshot{}, queue.ErrNotFound
	}
	if result.Error != nil {
		return queue.TaskSnapshot{}, fmt.Errorf("get queue task: %w", result.Error)
	}
	record, err := entity.QueueModel().record()
	if err != nil {
		return queue.TaskSnapshot{}, err
	}
	summary, err := entity.QueueModel().summary(time.Now().UTC(), r.timestampPrecision)
	if err != nil {
		return queue.TaskSnapshot{}, err
	}
	return queue.TaskSnapshot{Summary: summary, Task: record.Task.Clone()}, nil
}

// DeleteTask 仅删除 failed 或 retained completed 记录。
func (r *Repo[T]) DeleteTask(ctx context.Context, id string) error {
	result := r.consumerDB(ctx).Where("id = ? AND status IN ?", id, []Status{StatusFailed, StatusCompleted}).Delete(r.entity())
	return r.operationsTransitionResult(ctx, result, id, "delete")
}

// CancelTask 仅删除尚未持有有效租约的 pending/scheduled 记录。
func (r *Repo[T]) CancelTask(ctx context.Context, id string) error {
	now := time.Now().UTC().UnixMilli()
	result := r.consumerDB(ctx).
		Where("id = ? AND status NOT IN ? AND (reserved_until = 0 OR reserved_until <= ?)", id, []Status{StatusFailed, StatusCompleted}, now).
		Delete(r.entity())
	return r.operationsTransitionResult(ctx, result, id, "cancel")
}

// RetryTask 原子重置 failed 任务；其他状态返回 ErrStateConflict。
func (r *Repo[T]) RetryTask(ctx context.Context, id string, at time.Time) error {
	if err := r.validateTimestamp("available_at", at); err != nil {
		return err
	}
	result := r.consumerDB(ctx).Where("id = ? AND status = ? AND failed = ?", id, StatusFailed, true).Updates(r.retryValues(at))
	return r.operationsTransitionResult(ctx, result, id, "retry")
}

// CleanupTasks 在一个事务内选择并条件删除至多 Limit 条过期终态记录。
func (r *Repo[T]) CleanupTasks(ctx context.Context, options queue.CleanupOptions) (queue.CleanupResult, error) {
	if err := options.Validate(); err != nil {
		return queue.CleanupResult{}, err
	}
	condition, args := cleanupCondition(options, r.timestampPrecision)
	result := queue.CleanupResult{}
	err := r.consumerDB(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []string
		query := tx.Model(r.entity()).Table(r.table).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(condition, args...).Order("id ASC").Limit(options.Limit).Pluck("id", &ids)
		if query.Error != nil {
			return fmt.Errorf("select queue cleanup tasks: %w", query.Error)
		}
		if len(ids) == 0 {
			return nil
		}
		deleted := tx.Model(r.entity()).Table(r.table).Where("id IN ?", ids).Where(condition, args...).Delete(r.entity())
		if deleted.Error != nil {
			return fmt.Errorf("delete queue cleanup tasks: %w", deleted.Error)
		}
		result.Deleted = int(deleted.RowsAffected)
		return nil
	})
	return result, err
}

func (r *Repo[T]) operationsTransitionResult(ctx context.Context, result *gorm.DB, id, operation string) error {
	if result.Error != nil {
		return fmt.Errorf("%s queue task: %w", operation, result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}
	var count int64
	if err := r.consumerDB(ctx).Where("id = ?", id).Count(&count).Error; err != nil {
		return fmt.Errorf("classify %s queue task: %w", operation, err)
	}
	if count == 0 {
		return queue.ErrNotFound
	}
	return queue.ErrStateConflict
}

func (m *Model) summary(now time.Time, precision time.Duration) (queue.TaskSummary, error) {
	record, err := m.record()
	if err != nil {
		return queue.TaskSummary{}, err
	}
	summary := queue.TaskSummary{
		ID:             record.Task.ID,
		MessageVersion: record.Task.MessageVersion,
		Status:         m.operationStatus(now, precision),
		Attempts:       m.Attempts,
		AvailableAt:    record.Task.AvailableAt,
		CreatedAt:      record.Task.CreatedAt,
		FailureReason:  m.FailureReason,
	}
	if m.ReservedUntil != 0 {
		summary.ReservedUntil = time.UnixMilli(m.ReservedUntil).UTC()
	}
	if m.FailedAt != nil {
		summary.FailedAt = m.FailedAt.UTC()
	}
	if m.CompletedAt != nil {
		summary.CompletedAt = m.CompletedAt.UTC()
	}
	if m.UpdatedAt != nil {
		summary.UpdatedAt = m.UpdatedAt.UTC()
	}
	if m.StartedAt != nil {
		summary.StartedAt = m.StartedAt.UTC()
	}
	summary.LastError, summary.LastErrorAt = record.LastError, record.LastErrorAt
	return summary, nil
}

func (m *Model) operationStatus(now time.Time, precision time.Duration) queue.TaskStatus {
	if m.Status == StatusCompleted {
		return queue.TaskStatusCompleted
	}
	if m.Status == StatusFailed || m.Failed {
		return queue.TaskStatusFailed
	}
	if m.Token != "" && m.ReservedUntil > now.UnixMilli() {
		return queue.TaskStatusRunning
	}
	if m.AvailableAt.After(timestampFloor(now, precision)) {
		return queue.TaskStatusScheduled
	}
	return queue.TaskStatusPending
}

func operationsStatusCondition(statuses []queue.TaskStatus, now time.Time, precision time.Duration) (string, []any) {
	parts := make([]string, 0, len(statuses))
	args := make([]any, 0, len(statuses)*2)
	seen := make(map[queue.TaskStatus]struct{}, len(statuses))
	for _, status := range statuses {
		if _, ok := seen[status]; ok {
			continue
		}
		seen[status] = struct{}{}
		condition, values := statusCondition(status, now, precision)
		parts = append(parts, condition)
		args = append(args, values...)
	}
	return "(" + strings.Join(parts, ") OR (") + ")", args
}

// statusCondition 返回单个运维状态的 SQL 条件，与 operationStatus 的判定一致。
func statusCondition(status queue.TaskStatus, now time.Time, precision time.Duration) (string, []any) {
	leaseNow := now.UnixMilli()
	availableNow := timestampFloor(now, precision)
	active := []Status{StatusFailed, StatusCompleted}
	switch status {
	case queue.TaskStatusFailed:
		return "status = ?", []any{StatusFailed}
	case queue.TaskStatusCompleted:
		return "status = ?", []any{StatusCompleted}
	case queue.TaskStatusRunning:
		return "status NOT IN ? AND token <> '' AND reserved_until > ?", []any{active, leaseNow}
	case queue.TaskStatusScheduled:
		return "status NOT IN ? AND (reserved_until = 0 OR reserved_until <= ?) AND available_at > ?", []any{active, leaseNow, availableNow}
	default:
		return "status NOT IN ? AND (reserved_until = 0 OR reserved_until <= ?) AND available_at <= ?", []any{active, leaseNow, availableNow}
	}
}

func cleanupCondition(options queue.CleanupOptions, precision time.Duration) (string, []any) {
	parts := make([]string, 0, len(options.Statuses))
	args := make([]any, 0, len(options.Statuses)*2)
	for _, status := range options.Statuses {
		switch status {
		case queue.TaskStatusFailed:
			parts = append(parts, "status = ? AND failed_at < ?")
			args = append(args, StatusFailed, timestampCeil(options.Before, precision))
		case queue.TaskStatusCompleted:
			parts = append(parts, "status = ? AND completed_at < ?")
			args = append(args, StatusCompleted, timestampCeil(options.Before, precision))
		}
	}
	return "(" + strings.Join(parts, ") OR (") + ")", args
}

func encodeOperationsCursor(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

func decodeOperationsCursor(cursor string) (string, error) {
	value, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(value) == 0 {
		return "", errors.New("invalid queue task cursor")
	}
	return string(value), nil
}

var _ databasequeue.OperationsRepo = (*Repo[*simpleModel])(nil)
