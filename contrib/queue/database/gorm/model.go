// Package gorm 提供可嵌入业务模型的 GORM 任务仓储；不注册或创建数据库连接。
package gorm

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// Status 表示任务最后一次持久化的执行状态；租约到期是否可领取仍由时间字段判断。
type Status string

// taskID 为不同方言选择保持逐字节唯一性的列类型。
type taskID string

func (taskID) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db.Dialector.Name() == "mysql" {
		return "VARBINARY(128)"
	}
	return "BLOB"
}

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
// 不定义 TableName；表名由业务模型提供。
// 联合索引分别覆盖失败列表的筛选/排序与统计列；composite 按实际表名生成，避免多表迁移冲突。
type Model struct {
	// ID 是唯一对外任务标识 Task.ID；二进制列避免排序规则改变大小写、音调或尾随空格的唯一性。
	// 保留的完成记录继续占用该 ID，防止重复入队。
	ID taskID `gorm:"column:id;primaryKey;index:,composite:queue_failed,priority:3"`
	// Generation 是每次插入重新生成的行世代令牌，不是任务 ID；它避免同 ID 删除重建后的 ABA 误更新。
	Generation string `gorm:"column:generation;size:36;not null"`
	// Data 只保存非索引消息字段，不重复保存 ID 和 AvailableAt。
	Data []byte `gorm:"column:data;not null"`
	// Attempts 是本轮累计领取次数，包含过期重领；人工 Retry 时清零。
	Attempts int `gorm:"column:attempts;not null"`
	// Token 是当前领取凭据，确认、释放及失败写入须匹配；到期未重领时仍可使用，释放后为空。
	Token string `gorm:"column:token;size:36;not null"`
	// AvailableAt 是 UTC 最早可执行时间；按当前方言 timestamp 精度向上取整。
	AvailableAt time.Time `gorm:"column:available_at;type:timestamp;not null;index;index:,composite:queue_stats,priority:3"`
	// ReservedUntil 是租约截止的 UTC Unix 毫秒，写入时向上取整；0 表示无租约，到期不自动改写 Status。
	ReservedUntil int64 `gorm:"column:reserved_until;not null;index:,composite:queue_stats,priority:4"`
	// Failed 保留原有失败标记，与 StatusFailed 同步；完成记录为 false，人工 Retry 时清除。
	Failed bool `gorm:"column:failed;not null;index;index:,composite:queue_failed,priority:1;index:,composite:queue_stats,priority:2"`
	// FailureReason 保存受控失败分类，最多 128 字节，不保存 Handler 错误原文；人工 Retry 时清空。
	FailureReason string `gorm:"column:failure_reason;size:128;not null"`
	// FailedAt 是 UTC 失败归档时间；未失败或人工 Retry 后为 NULL。
	FailedAt *time.Time `gorm:"column:failed_at;type:timestamp;index:,composite:queue_failed,priority:2"`
	// Status 保存 pending/running/completed/failed；随租约和失败字段一并更新，completed 不再参与领取。
	Status Status `gorm:"column:status;size:16;not null;default:pending;index;index:,composite:queue_stats,priority:1"`
	// CompletedAt 是 UTC 成功确认时间；未完成为 NULL。
	CompletedAt *time.Time `gorm:"column:completed_at;type:timestamp"`
}

// QueueModel 返回嵌入的队列字段，仅供仓储填充和读取。
func (m *Model) QueueModel() *Model { return m }

// Entity 约束业务模型的指针类型；TableName 必须返回固定的单表名。
type Entity interface {
	QueueModel() *Model
	TableName() string
}

// 截止时间向上取整，查询时钟向下取整，避免提前执行和回收。
func deadlineMillis(at time.Time) int64 {
	value := at.UnixMilli()
	if at.Nanosecond()%int(time.Millisecond) != 0 {
		value++
	}
	return value
}

func timestampCeil(at time.Time, precision time.Duration) time.Time {
	if at.IsZero() {
		return time.Unix(0, 0).UTC()
	}
	at = at.UTC()
	truncated := at.Truncate(precision)
	if !truncated.Equal(at) {
		truncated = truncated.Add(precision)
	}
	return truncated
}

func timestampFloor(at time.Time, precision time.Duration) time.Time {
	return at.UTC().Truncate(precision)
}

func timestampPointer(at time.Time, precision time.Duration) *time.Time {
	value := timestampCeil(at, precision)
	return &value
}

// taskData 只保存不参与筛选、排序或状态变更的消息正文。
type taskData struct {
	MessageVersion string            `json:"message_version"`
	Payload        []byte            `json:"payload,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
}

func (m *Model) record() (*databasequeue.TaskRecord, error) {
	var data taskData
	if err := json.Unmarshal(m.Data, &data); err != nil {
		return nil, errors.New("decode queue task: invalid stored payload")
	}
	if strings.TrimSpace(data.MessageVersion) == "" || strings.TrimSpace(string(m.ID)) == "" || len(m.ID) > 128 || m.Generation == "" || m.AvailableAt.IsZero() || m.Attempts < 0 {
		return nil, errors.New("decode queue task: invalid stored state")
	}
	task := queue.Task{ID: string(m.ID), MessageVersion: data.MessageVersion, Payload: data.Payload, Headers: data.Headers, AvailableAt: m.AvailableAt.UTC(), CreatedAt: data.CreatedAt}
	record := &databasequeue.TaskRecord{Task: task, Attempts: m.Attempts, Token: m.Token, Failed: m.Failed, FailureReason: m.FailureReason}
	if m.ReservedUntil != 0 {
		record.ReservedUntil = time.UnixMilli(m.ReservedUntil).UTC()
	}
	if m.Failed {
		if m.FailedAt == nil {
			return nil, errors.New("decode queue task: invalid stored state")
		}
		record.FailedAt = m.FailedAt.UTC()
	}
	return record, nil
}
