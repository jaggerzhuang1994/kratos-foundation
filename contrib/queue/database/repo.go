package database

import (
	"context"
	"time"
)

// Repo 是由业务实现且绑定单个队列的持久化仓储；表名和隔离方式由业务决定。
// 不同队列使用独立 Repo 实例，可各自映射到不同表。所有方法必须支持并发和 Context 取消。
// 除 Insert 可参与投递方事务外，状态变更成功返回必须已提交；Claim 只能读取已提交任务。
// 输入只读且不得在返回后持有可变引用；返回 TaskRecord 及其任务数据必须是独立快照。
// 序列化、数据库精度、索引、迁移、错误转换和资源生命周期由实现负责。
// 存储精度不足时，可执行时间和租约截止必须向上取整，不得提前执行或回收。
// 持久化数据损坏时应保留原文并隔离失败，返回诊断错误，不得静默丢弃或伪造合法记录。
type Repo interface {
	// Insert 保存记录；已有待执行/领取/失败 Task.ID 时返回 queue.ErrDuplicate。
	// 可复用 Context 中业务显式开启的同库事务；此时成功只表示已写入事务，
	// 最终持久化由外层提交决定，回滚必须同时撤销任务与业务数据。
	// 无外层事务时成功返回必须已提交；不得自行提交调用方事务。
	// 不允许覆盖式 upsert；Task.ID 必须逐字节比较，保留大小写及尾随空格。
	Insert(context.Context, *TaskRecord) error

	// Claim 原子选取非失败、Task.AvailableAt<=now，且租约为空或已到期的一条记录，
	// 写入 token、reservedUntil 并增加 Attempts，返回更新后的完整快照。
	// 无候选返回 nil,nil；竞争失败可重选或返回空，不能伪造成功领取。
	// 必须通过事务或包含旧身份/token/到期状态的条件更新保证原子性。
	// 过期重领也增加次数，不能覆盖仍有效的租约。
	Claim(ctx context.Context, now, reservedUntil time.Time, token string) (*TaskRecord, error)

	// DeleteReserved 仅在 Task.ID/Token 匹配、非失败且租约非空时原子删除。
	// 不匹配返回 queue.ErrLeaseLost；完成后 ID 可以复用，新领取必须使用新 token。
	DeleteReserved(ctx context.Context, taskID, token string) error

	// ReleaseReserved 使用同一所有权条件，原子设置 Task.AvailableAt，清空租约/token，
	// 保留任务其余数据和 Attempts。不匹配返回 queue.ErrLeaseLost。
	ReleaseReserved(ctx context.Context, taskID, token string, availableAt time.Time) error

	// FailReserved 使用相同所有权条件，原子设置 Failed/FailureReason/FailedAt，
	// 清空租约/token，保留任务和 Attempts。不匹配返回 queue.ErrLeaseLost。
	FailReserved(ctx context.Context, taskID, token, reason string, failedAt time.Time) error

	// ListFailed 返回当前 Repo 的失败记录，按 FailedAt、Task.ID 升序，最多 limit 条。
	// 无记录返回空切片，不能共享持久化实体或可变任务数据。
	ListFailed(ctx context.Context, limit int) ([]TaskRecord, error)

	// RetryFailed 原子修改失败任务：清除失败状态和租约/token，Attempts=0，
	// Task.AvailableAt=availableAt，保留其余任务数据。不存在返回 queue.ErrNotFound。
	RetryFailed(ctx context.Context, taskID string, availableAt time.Time) error
}
