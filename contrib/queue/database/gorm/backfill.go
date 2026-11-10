package gorm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// backfillPending 选出 v2.13 之前写入、新列仍为空的行。
const backfillPending = "message_version IS NULL OR created_at IS NULL OR updated_at IS NULL"

// Backfill 为旧版本写入的行补齐 message_version、created_at、updated_at，返回本批实际更新的行数；
// 调用方循环直到返回 0。全部实例升级到 v2.13 之后执行（滚动升级期间旧版本仍会写入空列的行）。
//
// 取值：message_version 与 created_at 来自正文（正文损坏时消息版本记为空字符串、创建时间取 available_at）；
// updated_at 取创建、失败、完成、最近错误时间中最晚的一个。started_at 无从得知，保持 NULL。
// 每列以 COALESCE 写入，不覆盖并发写入的新值；按 id 与 generation 条件更新，不受同 ID 重建影响。
// 不开启事务、不加锁，可以在业务运行时分批执行。
func (r *Repo[T]) Backfill(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 1000 {
		return 0, errors.New("queue backfill limit must be between 1 and 1000")
	}
	var entities []T
	if err := r.consumerDB(ctx).Where(backfillPending).Order("id ASC").Limit(limit).Find(&entities).Error; err != nil {
		return 0, fmt.Errorf("select queue backfill tasks: %w", err)
	}
	updated := 0
	for _, entity := range entities {
		model := entity.QueueModel()
		values := r.backfillValues(model)
		result := r.consumerDB(ctx).Where("id = ? AND generation = ?", model.ID, model.Generation).Where(backfillPending).Updates(values)
		if result.Error != nil {
			return updated, fmt.Errorf("backfill queue task: %w", result.Error)
		}
		updated += int(result.RowsAffected)
	}
	return updated, nil
}

func (r *Repo[T]) backfillValues(m *Model) map[string]any {
	var data taskData
	decoded := json.Unmarshal(m.Data, &data) == nil
	version := ""
	if decoded && len(data.MessageVersion) <= maxMessageVersionBytes {
		version = data.MessageVersion
	}
	createdAt := m.AvailableAt.UTC()
	if decoded && !data.CreatedAt.IsZero() && r.validateTimestamp("created_at", data.CreatedAt) == nil {
		createdAt = data.CreatedAt
	}
	created := timestampFloor(createdAt, r.timestampPrecision)
	updatedAt := created
	for _, at := range []*time.Time{m.FailedAt, m.CompletedAt, m.LastErrorAt, m.CreatedAt} {
		if at != nil && at.After(updatedAt) {
			updatedAt = at.UTC()
		}
	}
	return map[string]any{
		"message_version": gorm.Expr("COALESCE(message_version, ?)", version),
		"created_at":      gorm.Expr("COALESCE(created_at, ?)", created),
		"updated_at":      gorm.Expr("COALESCE(updated_at, ?)", updatedAt),
	}
}
