package gorm

import (
	"context"
	"database/sql/driver"
	"fmt"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

type nullableTimestamp struct {
	Time  time.Time
	Valid bool
}

func (t nullableTimestamp) Value() (driver.Value, error) {
	if !t.Valid {
		return nil, nil
	}
	return t.Time, nil
}

// Scan 兼容驱动对普通时间列和聚合时间表达式返回不同底层类型的行为。
func (t *nullableTimestamp) Scan(value any) error {
	if value == nil {
		t.Time, t.Valid = time.Time{}, false
		return nil
	}
	if parsed, ok := value.(time.Time); ok {
		t.Time, t.Valid = parsed.UTC(), true
		return nil
	}
	var text string
	switch typed := value.(type) {
	case string:
		text = typed
	case []byte:
		text = string(typed)
	default:
		return fmt.Errorf("scan queue timestamp from %T", value)
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999"} {
		parsed, err := time.ParseInLocation(layout, text, time.UTC)
		if err == nil {
			t.Time, t.Valid = parsed.UTC(), true
			return nil
		}
	}
	return fmt.Errorf("scan queue timestamp: invalid database value")
}

// Stats 在单条聚合查询中读取互斥状态及最老 Ready 的 AvailableAt。
// 不开启事务、不加锁、不读取载荷；连接须与消费方法一样不包含外层事务。
func (r *Repo[T]) Stats(ctx context.Context, now time.Time) (queue.Stats, error) {
	var row struct {
		// Ready 统计到期且无有效租约的可执行任务数。
		Ready int64
		// Scheduled 统计未来到期且无有效租约的任务数。
		Scheduled int64
		// Running 统计仍持有有效租约的任务数。
		Running int64
		// Failed 统计归档失败任务数。
		Failed int64
		// Oldest 保存最早就绪时间；无就绪任务时为 NULL。
		Oldest nullableTimestamp
	}
	// 条件与 Claim 一致，过期租约计入 ready，尚未提交的业务写入不对采集可见。
	query := `COALESCE(SUM(CASE WHEN failed = ? AND available_at <= ? AND (reserved_until = 0 OR reserved_until <= ?) THEN 1 ELSE 0 END), 0) AS ready,
 COALESCE(SUM(CASE WHEN failed = ? AND available_at > ? AND (reserved_until = 0 OR reserved_until <= ?) THEN 1 ELSE 0 END), 0) AS scheduled,
 COALESCE(SUM(CASE WHEN failed = ? AND reserved_until > ? THEN 1 ELSE 0 END), 0) AS running,
 COALESCE(SUM(CASE WHEN failed = ? THEN 1 ELSE 0 END), 0) AS failed,
 MIN(CASE WHEN failed = ? AND available_at <= ? AND (reserved_until = 0 OR reserved_until <= ?) THEN available_at END) AS oldest`
	availableNow := timestampFloor(now, r.timestampPrecision)
	leaseNow := now.UnixMilli()
	if err := r.consumerDB(ctx).Where("status <> ?", StatusCompleted).Select(query, false, availableNow, leaseNow, false, availableNow, leaseNow, false, leaseNow, true, false, availableNow, leaseNow).Scan(&row).Error; err != nil {
		return queue.Stats{}, fmt.Errorf("read queue database stats: %w", err)
	}
	result := queue.Stats{Ready: row.Ready, Scheduled: row.Scheduled, Running: row.Running, Failed: row.Failed, OldestReadyKnown: true}
	if row.Oldest.Valid {
		result.OldestReadyAt = row.Oldest.Time.UTC()
	}
	return result, nil
}
