package queue

import (
	"context"
	"errors"
	"maps"
	"time"
)

// Task 是持久化任务；完成后是否保留由 Store 实现与配置决定。
type Task struct {
	// ID 在同队列的存储记录内唯一，长度为 1–128 字节且不能全为空白。
	ID string
	// MessageVersion 保存匹配 Handler 的消息契约版本标识，例如 EmailMessage.v1。
	MessageVersion string
	// Payload 保存 Codec 编码的业务消息。
	Payload []byte
	// Headers 保存业务元数据及跨进程追踪信息。
	Headers map[string]string
	// AvailableAt 是最早可领取时间，投递时零值补为当前时间；重试排期会更新此值。
	AvailableAt time.Time
	// CreatedAt 是任务创建时间，投递时零值补为当前时间。
	CreatedAt time.Time
}

// Clone 深复制任务数据；调用方和 Handler 可以独立修改副本。
func (t *Task) Clone() *Task {
	if t == nil {
		return nil
	}
	cpy := *t
	cpy.Payload = append([]byte(nil), t.Payload...)
	cpy.Headers = maps.Clone(t.Headers)
	return &cpy
}

// Reservation 是一次领取的只读快照。
type Reservation struct {
	// Task 是本次记录的独立任务快照。
	Task *Task
	// Token 标识本次租约；确认、释放和归档必须校验它以拒绝旧领取者。
	Token string
	// Attempts 为累计领取次数，包含本次及领取后未执行即崩溃的次数。
	Attempts int
}

// FailedTask 是失败存储的独立快照。
type FailedTask struct {
	// Task 是本次记录的独立任务快照。
	Task *Task
	// Attempts 为累计领取次数，包含本次及领取后未执行即崩溃的次数。
	Attempts int
	// Reason 为受控失败分类，不包含处理错误原文。
	Reason string
	// FailedAt 是最终失败归档时间。
	FailedAt time.Time
}

var (
	// ErrLeaseLost 表示任务已被重新领取或完成，旧领取者不得修改状态。
	ErrLeaseLost = errors.New("queue lease lost")
	// ErrDuplicate 表示同队列已存在相同 ID 的存储记录，包括后端保留的完成任务。
	ErrDuplicate = errors.New("queue task already exists")
	// ErrNotFound 表示所请求的任务不存在。
	ErrNotFound = errors.New("queue task not found")
)

// Store 是一个具名队列的持久化后端。所有方法必须支持并发与 Context 取消。
// Reserve 原子增加次数并生成新 token；无到期任务返回 nil, nil。
// Ack/Release/Fail 必须比较 token，不能删除或覆盖其他领取者的状态。
// 输入在调用期间只读，返回值为独立副本。Store 不拥有借用的数据库连接。
type Store interface {
	// Enqueue 保存准备好的任务，ID 与 MessageVersion 必填；重复的已存储 ID 返回 ErrDuplicate（包括后端保留的完成记录）。
	// 支持业务事务的后端可复用 ctx 中的事务，成功不代表外层已提交；最终以提交结果为准。
	Enqueue(context.Context, *Task) error
	// Reserve 使用传入时钟领取任务；lease 至少 1ms，截止时间向上取整。
	Reserve(context.Context, time.Time, time.Duration) (*Reservation, error)
	// Ack 确认当前租约任务完成；后端可删除或保留记录，过期但未重新分配的 token 仍可确认。
	Ack(context.Context, *Reservation) error
	// Release 保留尝试次数并保存最早下次执行时间。
	Release(context.Context, *Reservation, time.Time) error
	// Fail 保存最多 128 字节的受控原因分类及失败时间。
	Fail(context.Context, *Reservation, string, time.Time) error
	// Failed 查询最多 1–1000 条失败快照，不提供分页。
	Failed(context.Context, int) ([]FailedTask, error)
	// Retry 将失败任务重新排期并清零领取次数，不存在时返回 ErrNotFound。
	Retry(context.Context, string, time.Time) error
}

// Stats 是单个队列在采样时钟下的独立只读统计快照；各状态互斥，不计已完成记录。
type Stats struct {
	// Ready 为已到期或租约已过期、可再次领取的任务数。
	Ready int64
	// Scheduled 为尚未到期且未领取的任务数。
	Scheduled int64
	// Running 为租约仍有效的任务数。
	Running int64
	// Failed 为已归档且等待人工重试的任务数。
	Failed int64
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
