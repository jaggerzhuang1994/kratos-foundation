package gorm

import (
	"context"
	"fmt"
	"time"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"gorm.io/gorm"
)

func (r *Repo[T]) reserved(ctx context.Context, id, token string) *gorm.DB {
	return r.consumerDB(ctx).Where("id = ? AND token = ? AND token <> '' AND failed = ? AND reserved_until <> 0", taskKey(id), token, false)
}

func transitionResult(result *gorm.DB, operation string, missing error) error {
	if result.Error != nil {
		return fmt.Errorf("%s queue task: %w", operation, result.Error)
	}
	if result.RowsAffected == 0 {
		return missing
	}
	return nil
}

// DeleteReserved 仅删除当前 token 持有的记录；业务模型不得启用软删除。
func (r *Repo[T]) DeleteReserved(ctx context.Context, id, token string) error {
	return transitionResult(r.reserved(ctx, id, token).Delete(r.entity()), "delete", queue.ErrLeaseLost)
}

// ReleaseReserved 原子释放租约并排期，保留自定义字段与领取次数。
func (r *Repo[T]) ReleaseReserved(ctx context.Context, id, token string, at time.Time) error {
	return transitionResult(r.reserved(ctx, id, token).Updates(map[string]any{"available_at": deadlineMillis(at), "reserved_until": 0, "token": ""}), "release", queue.ErrLeaseLost)
}

// FailReserved 保留任务和自定义字段，在同一行记录失败状态。
func (r *Repo[T]) FailReserved(ctx context.Context, id, token, reason string, at time.Time) error {
	return transitionResult(r.reserved(ctx, id, token).Updates(map[string]any{"failed": true, "failure_reason": reason, "failed_at": at.UnixMilli(), "reserved_until": 0, "token": ""}), "fail", queue.ErrLeaseLost)
}

// ListFailed 按失败时间和任务 ID 返回独立快照；损坏正文返回错误而不静默跳过。
func (r *Repo[T]) ListFailed(ctx context.Context, limit int) ([]databasequeue.TaskRecord, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("queue failed limit must be between 1 and 1000")
	}
	var entities []T
	if err := r.consumerDB(ctx).Where("failed = ?", true).Order("failed_at ASC, id ASC").Limit(limit).Find(&entities).Error; err != nil {
		return nil, fmt.Errorf("list failed queue tasks: %w", err)
	}
	records := make([]databasequeue.TaskRecord, 0, len(entities))
	for _, entity := range entities {
		record, err := entity.QueueModel().record()
		if err != nil {
			return nil, err
		}
		records = append(records, *record)
	}
	return records, nil
}

// RetryFailed 原子重置失败状态、次数和租约，保留正文及自定义字段。
func (r *Repo[T]) RetryFailed(ctx context.Context, id string, at time.Time) error {
	return transitionResult(r.consumerDB(ctx).Where("id = ? AND failed = ?", taskKey(id), true).Updates(map[string]any{"failed": false, "failure_reason": "", "failed_at": 0, "attempts": 0, "token": "", "reserved_until": 0, "available_at": deadlineMillis(at)}), "retry", queue.ErrNotFound)
}
