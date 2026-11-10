package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	// UpdatedAt 是最近一次状态变化（入队、领取、释放、失败、完成、人工重试或改排期）的时间；
	// 续租不更新。后端未记录时为零值。
	UpdatedAt time.Time
	// StartedAt 是最近一次开始执行（领取）的时间；从未执行或后端未记录时为零值。
	StartedAt time.Time
	// ReservedUntil 是当前租约截止时间；无租约时为零值。
	ReservedUntil time.Time
	// FailedAt 是终态失败时间；非失败任务为零值。
	FailedAt time.Time
	// CompletedAt 是保留成功记录的完成时间；其他任务为零值。
	CompletedAt time.Time
	// FailureReason 是受控失败分类，不应包含原始敏感错误。
	FailureReason string
	// LastError 是最近一次执行失败的错误原文；成功或人工重试后仍保留，直到下一次失败覆盖。
	// 后端未记录时为空。
	LastError string
	// LastErrorAt 是 LastError 的记录时间；未记录时为零值。
	LastErrorAt time.Time
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
	// ErrUnsupported 表示后端不支持请求的查询条件、排序或运维能力。
	ErrUnsupported = errors.New("queue operation not supported by store")
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

// MaxBulkTasks 是批量运维单次处理的最大任务数。
const MaxBulkTasks = 1000

// TaskGroupField 是分组统计的字段。
type TaskGroupField string

const (
	// TaskGroupMessageVersion 按消息契约标识分组。
	TaskGroupMessageVersion TaskGroupField = "message_version"
	// TaskGroupFailureReason 按受控失败分类分组；未失败任务的分类为空字符串。
	TaskGroupFailureReason TaskGroupField = "failure_reason"
)

// TaskGroup 是分组统计的一组结果。
type TaskGroup struct {
	// Value 是分组字段取值；后端未记录时为空字符串。
	Value string
	// Count 是该组任务数。
	Count int64
}

// BulkItemResult 是按 ID 批量操作中单个任务的结果。
type BulkItemResult struct {
	// ID 是请求中的任务 ID。
	ID string
	// Err 为 nil 表示成功；不存在为 ErrNotFound，状态不允许为 ErrStateConflict。
	Err error
}

// BulkResult 是批量运维的结果。
type BulkResult struct {
	// Affected 是实际变更的任务数。
	Affected int
	// Items 按请求顺序给出每个 ID 的结果；按条件批量操作时为空。
	Items []BulkItemResult
}

// ExtendedOperations 是 Operations 的可选扩展，提供运维后台常用的筛选排序、计数、分组、改排期与批量操作。
// 通过 Queue.Operations() 取得后类型断言判断后端是否支持：
//
//	ops, ok := q.Operations()
//	ext, extended := ops.(queue.ExtendedOperations)
//
// 所有变更都在存储层按实际状态与租约原子校验；按条件批量操作每次最多处理 limit（1–MaxBulkTasks）条，
// 调用方循环直到 Affected 为 0 即可处理完全部匹配项。
type ExtendedOperations interface {
	Operations
	// Count 返回匹配筛选条件的任务数。
	Count(context.Context, TaskFilter) (int64, error)
	// CountByStatus 一次返回各状态的任务数；filter.Statuses 必须为空，未出现的状态计 0。
	CountByStatus(context.Context, TaskFilter) (map[TaskStatus]int64, error)
	// Group 按字段分组计数，按数量降序、取值升序返回至多 limit（1–1000）组。
	Group(ctx context.Context, filter TaskFilter, field TaskGroupField, limit int) ([]TaskGroup, error)
	// Reschedule 修改尚未持有有效租约的 pending/scheduled 任务的最早执行时间（立即执行或延后），
	// 不改变尝试次数；其他状态返回 ErrStateConflict。
	Reschedule(ctx context.Context, id string, at time.Time) error
	// RetryTasks 按 ID 重试 failed 任务，规则同 Retry；最多 MaxBulkTasks 个。
	RetryTasks(ctx context.Context, ids []string, at time.Time) (BulkResult, error)
	// CancelTasks 按 ID 取消任务，规则同 Cancel；最多 MaxBulkTasks 个。
	CancelTasks(ctx context.Context, ids []string) (BulkResult, error)
	// DeleteTasks 按 ID 删除终态任务，规则同 Delete；最多 MaxBulkTasks 个。
	DeleteTasks(ctx context.Context, ids []string) (BulkResult, error)
	// RescheduleTasks 按 ID 改排期，规则同 Reschedule；最多 MaxBulkTasks 个。
	RescheduleTasks(ctx context.Context, ids []string, at time.Time) (BulkResult, error)
	// RetryMatching 重试匹配条件的 failed 任务（其他状态忽略），至多 limit 条。
	RetryMatching(ctx context.Context, filter TaskFilter, at time.Time, limit int) (BulkResult, error)
	// CancelMatching 取消匹配条件、没有有效租约的 pending/scheduled 任务，至多 limit 条。
	CancelMatching(ctx context.Context, filter TaskFilter, limit int) (BulkResult, error)
	// DeleteMatching 删除匹配条件的 failed/completed 任务，至多 limit 条。
	DeleteMatching(ctx context.Context, filter TaskFilter, limit int) (BulkResult, error)
}

// ValidateBulkIDs 校验按 ID 批量操作的输入：1–MaxBulkTasks 个非空、不超过 128 字节的 ID。
func ValidateBulkIDs(ids []string) error {
	if len(ids) < 1 || len(ids) > MaxBulkTasks {
		return fmt.Errorf("queue bulk operation requires 1 to %d ids", MaxBulkTasks)
	}
	for _, id := range ids {
		if len(id) > 128 || strings.TrimSpace(id) == "" {
			return errors.New("queue bulk operation contains an invalid id")
		}
	}
	return nil
}

// ValidateBulkLimit 校验按条件批量操作的单次上限。
func ValidateBulkLimit(limit int) error {
	if limit < 1 || limit > MaxBulkTasks {
		return fmt.Errorf("queue bulk operation limit must be between 1 and %d", MaxBulkTasks)
	}
	return nil
}
