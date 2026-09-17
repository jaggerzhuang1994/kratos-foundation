package queue

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// TaskStatus 描述运维视角下互斥的持久化任务状态。
type TaskStatus string

const (
	// TaskStatusPending 表示任务当前可领取。
	TaskStatusPending TaskStatus = "pending"
	// TaskStatusScheduled 表示任务尚未到排期时间。
	TaskStatusScheduled TaskStatus = "scheduled"
	// TaskStatusRunning 表示任务持有有效租约。
	TaskStatusRunning TaskStatus = "running"
	// TaskStatusFailed 表示任务已进入终态失败。
	TaskStatusFailed TaskStatus = "failed"
	// TaskStatusCompleted 表示后端保留了成功记录。
	TaskStatusCompleted TaskStatus = "completed"
)

// Valid 报告状态是否属于公共运维契约。
func (s TaskStatus) Valid() bool {
	switch s {
	case TaskStatusPending, TaskStatusScheduled, TaskStatusRunning, TaskStatusFailed, TaskStatusCompleted:
		return true
	default:
		return false
	}
}

// TaskQuery 定义有界的任务元数据分页查询；Cursor 由后端生成且不得解析。
type TaskQuery struct {
	// Statuses 为空时查询后端支持的全部状态。
	Statuses []TaskStatus
	// Cursor 是上一次 List 返回的后端不透明游标。
	Cursor string
	// Limit 限制本页返回数量，范围为 1–1000。
	Limit int
}

// Validate 校验分页上限和状态过滤条件。
func (q TaskQuery) Validate() error {
	if q.Limit < 1 || q.Limit > 1000 {
		return errors.New("queue task query limit must be between 1 and 1000")
	}
	for _, status := range q.Statuses {
		if !status.Valid() {
			return fmt.Errorf("queue task query contains invalid status %q", status)
		}
	}
	return nil
}

// TaskSummary 是不包含 Payload 与 Headers 的任务元数据快照。
type TaskSummary struct {
	// ID 是持久任务标识。
	ID string
	// MessageVersion 是类型化消息契约标识。
	MessageVersion string
	// Status 是查询时计算的互斥任务状态。
	Status TaskStatus
	// Attempts 是本轮累计领取次数。
	Attempts int
	// AvailableAt 是任务最早可领取时间。
	AvailableAt time.Time
	// CreatedAt 是任务创建时间。
	CreatedAt time.Time
	// ReservedUntil 是当前租约截止时间；无租约时为零值。
	ReservedUntil time.Time
	// FailedAt 是终态失败时间；非失败任务为零值。
	FailedAt time.Time
	// CompletedAt 是保留成功记录的完成时间；其他任务为零值。
	CompletedAt time.Time
	// FailureReason 是受控失败分类，不应包含原始敏感错误。
	FailureReason string
}

// TaskPage 保存一页任务元数据和下一页不透明游标；空游标表示没有下一页。
type TaskPage struct {
	// Tasks 是本页元数据独立快照。
	Tasks []TaskSummary
	// NextCursor 为空表示没有下一页。
	NextCursor string
}

// TaskSnapshot 是详情查询返回的独立快照；Task 包含完整正文。
type TaskSnapshot struct {
	// Summary 是任务元数据。
	Summary TaskSummary
	// Task 包含 Payload 与 Headers，返回值为独立副本。
	Task *Task
}

// Clone 深复制详情和任务正文。
func (s TaskSnapshot) Clone() TaskSnapshot {
	s.Task = s.Task.Clone()
	return s
}

// CleanupOptions 限定终态任务的保留期清理范围。
type CleanupOptions struct {
	// Before 只清理严格早于此时间的终态记录。
	Before time.Time
	// Limit 限制单次删除数量，范围为 1–1000。
	Limit int
	// Statuses 只能包含 failed 和 completed。
	Statuses []TaskStatus
}

// Validate 要求明确截止时间、1–1000 的上限及 failed/completed 状态。
func (o CleanupOptions) Validate() error {
	if o.Before.IsZero() {
		return errors.New("queue cleanup cutoff is required")
	}
	if o.Limit < 1 || o.Limit > 1000 {
		return errors.New("queue cleanup limit must be between 1 and 1000")
	}
	if len(o.Statuses) == 0 {
		return errors.New("queue cleanup requires terminal statuses")
	}
	for _, status := range o.Statuses {
		if status != TaskStatusFailed && status != TaskStatusCompleted {
			return fmt.Errorf("queue cleanup does not support status %q", status)
		}
	}
	return nil
}

// CleanupResult 报告本次有界清理实际删除的记录数。
type CleanupResult struct {
	// Deleted 是本次实际删除的记录数。
	Deleted int
}

var (
	// ErrStateConflict 表示任务存在，但当前状态或租约不允许请求的运维变更。
	ErrStateConflict = errors.New("queue task state conflict")
)

// Operations 提供构建业务运维 API 所需的可选存储能力。
// List 不返回任务正文；Get 返回的 Task 必须是独立副本。
type Operations interface {
	List(context.Context, TaskQuery) (TaskPage, error)
	Get(context.Context, string) (TaskSnapshot, error)
	Delete(context.Context, string) error
	Cancel(context.Context, string) error
	Retry(context.Context, string, time.Time) error
	Cleanup(context.Context, CleanupOptions) (CleanupResult, error)
}

// OperationsProvider 允许适配器根据其底层 Repo 动态暴露运维能力。
type OperationsProvider interface {
	QueueOperations() (Operations, bool)
}
