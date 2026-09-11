package gorm

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

// Stats 在单条聚合查询中读取互斥状态及最老 Ready 的 AvailableAt。
// 不开启事务、不加锁、不读取载荷；连接须与消费方法一样不包含外层事务。
func (r *Repo[T]) Stats(ctx context.Context, now time.Time) (queue.Stats, error) {
	var row struct {
		Ready     int64
		Scheduled int64
		Running   int64
		Failed    int64
		Oldest    sql.NullInt64
	}
	// 条件与 Claim 一致，过期租约计入 ready，尚未提交的业务写入不对采集可见。
	query := `COALESCE(SUM(CASE WHEN failed = ? AND available_at <= ? AND (reserved_until = 0 OR reserved_until <= ?) THEN 1 ELSE 0 END), 0) AS ready,
 COALESCE(SUM(CASE WHEN failed = ? AND available_at > ? AND (reserved_until = 0 OR reserved_until <= ?) THEN 1 ELSE 0 END), 0) AS scheduled,
 COALESCE(SUM(CASE WHEN failed = ? AND reserved_until > ? THEN 1 ELSE 0 END), 0) AS running,
 COALESCE(SUM(CASE WHEN failed = ? THEN 1 ELSE 0 END), 0) AS failed,
 MIN(CASE WHEN failed = ? AND available_at <= ? AND (reserved_until = 0 OR reserved_until <= ?) THEN available_at END) AS oldest`
	at := now.UnixMilli()
	if err := r.consumerDB(ctx).Select(query, false, at, at, false, at, at, false, at, true, false, at, at).Scan(&row).Error; err != nil {
		return queue.Stats{}, fmt.Errorf("read queue database stats: %w", err)
	}
	result := queue.Stats{Ready: row.Ready, Scheduled: row.Scheduled, Running: row.Running, Failed: row.Failed, OldestReadyKnown: true}
	if row.Oldest.Valid {
		result.OldestReadyAt = time.UnixMilli(row.Oldest.Int64).UTC()
	}
	return result, nil
}
