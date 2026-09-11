package database

import (
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

// TaskRecord 表示任务及其执行状态，是 Repo 与 Store 之间的业务契约。
// Task 是完整的投递载荷；数据表、主键、序列化格式及 ORM 实体由业务 Repo 自行定义。
// 返回的 Task.Payload/Headers 必须是独立副本，不能共享持久化层的可变缓冲区。
type TaskRecord struct {
	Task          queue.Task
	Attempts      int
	Token         string
	ReservedUntil time.Time
	Failed        bool
	FailureReason string
	FailedAt      time.Time
}
