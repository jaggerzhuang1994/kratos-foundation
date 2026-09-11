package queue

import (
	"context"
	"errors"
	"maps"
	"time"
)

// Task 是持久化任务；Type 选择业务 Handler，Payload 只保存可序列化数据。
// ID 为 1–128 字节，在同一队列的待执行/失败集合内唯一；完成后不保留去重记录。
type Task struct {
	ID          string
	Type        string
	Payload     []byte
	Headers     map[string]string
	AvailableAt time.Time
	CreatedAt   time.Time
}

// Clone 深复制任务数据；调用方和 Handler 可以独立修改副本。
func (t *Task) Clone() *Task {
	if t == nil {
		return nil
	}
	copy := *t
	copy.Payload = append([]byte(nil), t.Payload...)
	copy.Headers = maps.Clone(t.Headers)
	return &copy
}

// Reservation 是一次领取的只读快照；Token 标识本次租约，Attempts 包含本次领取。
type Reservation struct {
	Task     *Task
	Token    string
	Attempts int
}

// FailedTask 是失败存储的独立快照。Reason 使用受控分类，不存储 Handler 错误原文。
type FailedTask struct {
	Task     *Task
	Attempts int
	Reason   string
	FailedAt time.Time
}

var (
	// ErrLeaseLost 表示任务已被重新领取或完成，旧领取者不得修改状态。
	ErrLeaseLost = errors.New("queue lease lost")
	// ErrDuplicate 表示同队列中已经存在相同 ID 的待执行或失败任务。
	ErrDuplicate = errors.New("queue task already exists")
	// ErrNotFound 表示所请求的失败任务不存在。
	ErrNotFound = errors.New("queue task not found")
)

// Store 是一个具名队列的持久化后端。所有方法必须支持并发与 Context 取消。
// Reserve 原子增加次数并生成新 token；无到期任务返回 nil, nil。
// Ack/Release/Fail 必须比较 token，不能删除或覆盖其他领取者的状态。
// 输入在调用期间只读，返回值为独立副本。Store 不拥有借用的数据库连接。
type Store interface {
	// Enqueue 保存准备好的任务，ID 与 Type 必填；重复未完成 ID 返回 ErrDuplicate。
	// 支持业务事务的后端可复用 ctx 中的事务，成功不代表外层已提交；最终以提交结果为准。
	Enqueue(context.Context, *Task) error
	// Reserve 使用传入时钟领取任务；lease 至少 1ms，截止时间向上取整。
	Reserve(context.Context, time.Time, time.Duration) (*Reservation, error)
	// Ack 删除当前租约任务；过期但未重新分配的 token 仍可确认。
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
