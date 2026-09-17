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

// ListTasks 按稳定主键顺序返回一页任务元数据；正文仅由 GetTask 返回。
func (r *Repo[T]) ListTasks(ctx context.Context, query queue.TaskQuery) (queue.TaskPage, error) {
	if err := query.Validate(); err != nil {
		return queue.TaskPage{}, err
	}
	now := time.Now().UTC()
	db := r.consumerDB(ctx)
	if query.Cursor != "" {
		cursor, err := decodeOperationsCursor(query.Cursor)
		if err != nil {
			return queue.TaskPage{}, err
		}
		db = db.Where("id > ?", cursor)
	}
	if len(query.Statuses) > 0 {
		condition, args := operationsStatusCondition(query.Statuses, now, r.timestampPrecision)
		db = db.Where(condition, args...)
	}
	var entities []T
	if err := db.Order("id ASC").Limit(query.Limit + 1).Find(&entities).Error; err != nil {
		return queue.TaskPage{}, fmt.Errorf("list queue tasks: %w", err)
	}
	page := queue.TaskPage{Tasks: make([]queue.TaskSummary, 0, min(len(entities), query.Limit))}
	for index, entity := range entities {
		if index == query.Limit {
			page.NextCursor = encodeOperationsCursor(string(entities[index-1].QueueModel().ID))
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
	result := r.consumerDB(ctx).Where("id = ? AND status = ? AND failed = ?", id, StatusFailed, true).
		Updates(map[string]any{"status": StatusPending, "completed_at": nil, "failed": false, "failure_reason": "", "failed_at": nil, "attempts": 0, "token": "", "reserved_until": 0, "available_at": timestampCeil(at, r.timestampPrecision)})
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
	leaseNow := now.UnixMilli()
	availableNow := timestampFloor(now, precision)
	for _, status := range statuses {
		if _, ok := seen[status]; ok {
			continue
		}
		seen[status] = struct{}{}
		switch status {
		case queue.TaskStatusFailed:
			parts = append(parts, "status = ?")
			args = append(args, StatusFailed)
		case queue.TaskStatusCompleted:
			parts = append(parts, "status = ?")
			args = append(args, StatusCompleted)
		case queue.TaskStatusRunning:
			parts = append(parts, "status NOT IN ? AND token <> '' AND reserved_until > ?")
			args = append(args, []Status{StatusFailed, StatusCompleted}, leaseNow)
		case queue.TaskStatusScheduled:
			parts = append(parts, "status NOT IN ? AND (reserved_until = 0 OR reserved_until <= ?) AND available_at > ?")
			args = append(args, []Status{StatusFailed, StatusCompleted}, leaseNow, availableNow)
		case queue.TaskStatusPending:
			parts = append(parts, "status NOT IN ? AND (reserved_until = 0 OR reserved_until <= ?) AND available_at <= ?")
			args = append(args, []Status{StatusFailed, StatusCompleted}, leaseNow, availableNow)
		}
	}
	return "(" + strings.Join(parts, ") OR (") + ")", args
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
