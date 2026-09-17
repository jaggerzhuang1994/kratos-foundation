package database

import (
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

// TaskRecord 表示任务及其执行状态，是 Repo 与 Store 之间的业务契约。
// 数据表、主键、序列化格式及 ORM 实体由业务 Repo 自行定义。
type TaskRecord struct {
	// Task 保存完整投递载荷；AvailableAt 是当前最早执行时间，Payload/Headers 为独立副本。
	Task queue.Task
	// Attempts 是本轮累计领取次数，包含租约到期后的重领；人工 Retry 时清零。
	Attempts int
	// Token 标识本次领取所有权；每次领取重新生成，释放、失败或保留完成记录时清空。
	Token string
	// ReservedUntil 是租约截止时间；零值表示无租约，到期后仍须满足非失败和执行时间条件才能重领。
	ReservedUntil time.Time
	// Failed 表示最终失败或损坏数据隔离；普通重试不置为 true，人工 Retry 时清除。
	Failed bool
	// FailureReason 保存受控失败分类，不保存 Handler 错误原文；人工 Retry 时清空。
	FailureReason string
	// FailedAt 是失败归档时间，仅在 Failed 为 true 时有意义；人工 Retry 时恢复零值。
	FailedAt time.Time
}
