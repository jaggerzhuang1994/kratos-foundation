// Package gorm 提供可嵌入业务模型的 GORM 任务仓储；不注册或创建数据库连接。
package gorm

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

// Status 表示任务最后一次持久化的执行状态；租约到期是否可领取仍由时间字段判断。
type Status string

const (
	// StatusPending 表示待执行或等待重试的任务。
	StatusPending Status = "pending"
	// StatusRunning 表示已领取任务，不保证持有租约的进程仍在运行。
	StatusRunning Status = "running"
	// StatusCompleted 表示已确认成功并保留的任务。
	StatusCompleted Status = "completed"
	// StatusFailed 表示最终失败或数据损坏隔离的任务。
	StatusFailed Status = "failed"
)

// Model 是队列管理的存储字段。业务须按值匿名嵌入，不得覆盖字段、列名或添加软删除。
// 不定义 TableName；表名由业务模型提供。ID 是任务 ID 的十六进制编码，避免数据库
// 大小写、音调和尾随空格排序规则改变任务唯一性。Identity 隔离删除后重新插入的同 ID。
// 联合索引分别覆盖失败列表的筛选/排序与统计列；composite 按实际表名生成，避免多表迁移冲突。
// Status 随失败/租约字段一起更新；CompletedAt 是确认完成时的 UTC Unix 毫秒，未完成为 0。
type Model struct {
	// ID 是 Task.ID 的十六进制编码主键；保留的完成记录继续占用该 ID，防止重复入队。
	ID string `gorm:"column:id;primaryKey;size:256;index:,composite:queue_failed,priority:3"`
	// Identity 在每次插入时生成，用于区分删除后重新入队的同 ID，避免旧领取快照误更新新记录。
	Identity string `gorm:"column:identity;size:36;not null"`
	// Data 保存完整 queue.Task 的 JSON；重试排期不改写正文，读取时以 AvailableAt 列覆盖正文中的同名字段。
	Data []byte `gorm:"column:data;not null"`
	// Attempts 是本轮累计领取次数，包含过期重领；人工 Retry 时清零。
	Attempts int `gorm:"column:attempts;not null"`
	// Token 是当前领取凭据，用于确认、释放及失败写入的所有权校验；无租约时为空。
	Token string `gorm:"column:token;size:36;not null"`
	// AvailableAt 是最早可执行时间的 UTC Unix 毫秒；写入时向上取整，释放或人工 Retry 时更新。
	AvailableAt int64 `gorm:"column:available_at;not null;index;index:,composite:queue_stats,priority:3"`
	// ReservedUntil 是租约截止的 UTC Unix 毫秒，写入时向上取整；0 表示无租约，到期不自动改写 Status。
	ReservedUntil int64 `gorm:"column:reserved_until;not null;index:,composite:queue_stats,priority:4"`
	// Failed 保留原有失败标记，与 StatusFailed 同步；完成记录为 false，人工 Retry 时清除。
	Failed bool `gorm:"column:failed;not null;index;index:,composite:queue_failed,priority:1;index:,composite:queue_stats,priority:2"`
	// FailureReason 保存受控失败分类，最多 128 字节，不保存 Handler 错误原文；人工 Retry 时清空。
	FailureReason string `gorm:"column:failure_reason;size:128;not null"`
	// FailedAt 是失败归档时间的 UTC Unix 毫秒；仅 Failed 为 true 时有效，人工 Retry 时清零。
	FailedAt int64 `gorm:"column:failed_at;not null;index:,composite:queue_failed,priority:2"`
	// Status 保存 pending/running/completed/failed；随租约和失败字段一并更新，completed 不再参与领取。
	Status Status `gorm:"column:status;size:16;not null;default:pending;index;index:,composite:queue_stats,priority:1"`
	// CompletedAt 是成功确认时间的 UTC Unix 毫秒；仅保留成功记录时写入，未完成为 0。
	CompletedAt int64 `gorm:"column:completed_at;not null;default:0"`
}

// QueueModel 返回嵌入的队列字段，仅供仓储填充和读取。
func (m *Model) QueueModel() *Model { return m }

// Entity 约束业务模型的指针类型；TableName 必须返回固定的单表名。
type Entity interface {
	QueueModel() *Model
	TableName() string
}

func taskKey(id string) string { return hex.EncodeToString([]byte(id)) }

// 截止时间向上取整，查询时钟向下取整，避免提前执行和回收。
func deadlineMillis(at time.Time) int64 {
	value := at.UnixMilli()
	if at.Nanosecond()%int(time.Millisecond) != 0 {
		value++
	}
	return value
}

func (m *Model) record() (*databasequeue.TaskRecord, error) {
	var task queue.Task
	if err := json.Unmarshal(m.Data, &task); err != nil {
		return nil, errors.New("decode queue task: invalid stored payload")
	}
	if strings.TrimSpace(task.Type) == "" || strings.TrimSpace(task.ID) == "" || len(task.ID) > 128 || taskKey(task.ID) != m.ID || m.Identity == "" || m.Attempts < 0 {
		return nil, errors.New("decode queue task: invalid stored state")
	}
	task.AvailableAt = time.UnixMilli(m.AvailableAt).UTC()
	record := &databasequeue.TaskRecord{Task: task, Attempts: m.Attempts, Token: m.Token, Failed: m.Failed, FailureReason: m.FailureReason}
	if m.ReservedUntil != 0 {
		record.ReservedUntil = time.UnixMilli(m.ReservedUntil).UTC()
	}
	if m.Failed {
		record.FailedAt = time.UnixMilli(m.FailedAt).UTC()
	}
	return record, nil
}
