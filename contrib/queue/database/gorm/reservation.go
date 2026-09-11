package gorm

import (
	"context"
	"errors"
	"fmt"
	"time"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"gorm.io/gorm"
)

// Claim 使用读取快照后的条件更新竞争领取，只有更新一行的调用方取得租约。
// 不跨 Handler 持锁；竞争失败返回空，由 Worker 下一次轮询重选。
func (r *Repo[T]) Claim(ctx context.Context, now, until time.Time, token string) (*databasequeue.TaskRecord, error) {
	if token == "" || len(token) > 36 || !until.After(now) {
		return nil, errors.New("invalid queue lease")
	}
	entity := r.entity()
	available := r.consumerDB(ctx).Where("failed = ? AND available_at <= ? AND (reserved_until = 0 OR reserved_until <= ?)", false, now.UnixMilli(), now.UnixMilli())
	if err := available.Order("available_at ASC, id ASC").Take(entity).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("select queue task: %w", err)
	}
	model := entity.QueueModel()
	record, decodeErr := model.record()
	// Identity 防止同 ID 删除重建的 ABA；Token/Attempts/截止防止覆盖已变化的快照。
	claim := r.consumerDB(ctx).Where("id = ? AND identity = ? AND token = ? AND attempts = ? AND reserved_until = ? AND available_at = ? AND failed = ?", model.ID, model.Identity, model.Token, model.Attempts, model.ReservedUntil, model.AvailableAt, false)
	if decodeErr != nil {
		result := claim.Updates(map[string]any{"failed": true, "failure_reason": "corrupt_payload", "failed_at": now.UnixMilli(), "token": "", "reserved_until": 0})
		if result.Error != nil {
			return nil, errors.Join(decodeErr, fmt.Errorf("quarantine queue task: %w", result.Error))
		}
		if result.RowsAffected == 0 {
			return nil, nil
		}
		return nil, decodeErr
	}
	result := claim.Updates(map[string]any{"token": token, "reserved_until": deadlineMillis(until), "attempts": model.Attempts + 1})
	if result.Error != nil {
		return nil, fmt.Errorf("claim queue task: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	record.Token = token
	record.Attempts++
	record.ReservedUntil = time.UnixMilli(deadlineMillis(until)).UTC()
	return record, nil
}
