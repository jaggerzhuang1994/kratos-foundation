package queue

import (
	"context"
	"time"
)

// Stats 是单个队列在采样时钟下的独立只读统计快照；各状态互斥。
// Ready 包含已到期的排期任务及租约已过期的任务；Running 只包含有效租约。
// Scheduled 为尚未到期且未领取的任务；Failed 为等待人工重试的任务。
type Stats struct {
	Ready     int64
	Scheduled int64
	Running   int64
	Failed    int64
	// OldestReadyAt 是 Ready 任务中最早的当前 AvailableAt，并非 CreatedAt。
	// 重试/Release 会重置 AvailableAt；租约过期后仍使用任务的 AvailableAt。
	OldestReadyAt time.Time
	// OldestReadyKnown 为 false 表示后端无法在有界开销内精确取得年龄。
	// Ready 为零且本字段为 true 时，年龄为零，OldestReadyAt 无意义。
	OldestReadyKnown bool
}

// StatsProvider 是 Store 或 Repo 可选的只读统计能力，不扩展 Store 的必需方法。
// 实现必须并发安全并遵守 ctx 取消；失败返回 error，不得用空快照伪装空队列。
type StatsProvider interface {
	Stats(ctx context.Context, now time.Time) (Stats, error)
}
