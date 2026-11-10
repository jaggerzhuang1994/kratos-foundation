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
// 联合索引分别覆盖失败列表、统计、按状态的创建/更新时间排序；composite 按实际表名生成，避免多表迁移冲突。
// v2.13 新增的 message_version、created_at、updated_at、started_at 均可空：旧版本写入的行在回填前为 NULL，
// 读取时创建时间与消息版本退回正文里的值，见 Backfill。
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
	Status Status `gorm:"column:status;size:16;not null;default:pending;index;index:,composite:queue_stats,priority:1;index:,composite:queue_created,priority:1;index:,composite:queue_updated,priority:1"`
	// CompletedAt 是 UTC 成功确认时间；未完成为 NULL。
	CompletedAt *time.Time `gorm:"column:completed_at;type:timestamp"`
	// LastErrorMessage 是最近一次执行失败的错误原文（最多 queue.MaxErrorMessageBytes 字节）；
	// 重试释放和失败归档时覆盖写入，成功确认与人工 Retry 不清除；从未失败为 NULL。
	LastErrorMessage *string `gorm:"column:last_error_message;type:text"`
	// LastErrorAt 是 LastErrorMessage 的 UTC 记录时间；从未失败为 NULL。
	LastErrorAt *time.Time `gorm:"column:last_error_at;type:timestamp"`
	// MessageVersion 冗余保存正文里的消息契约标识（最多 255 字节），供筛选与分组；旧行回填前为 NULL。
	MessageVersion *string `gorm:"column:message_version;size:255;index"`
	// CreatedAt 是任务创建时间（Task.CreatedAt，按精度向下取整）；旧行回填前为 NULL。
	CreatedAt *time.Time `gorm:"column:created_at;type:timestamp;autoCreateTime:false;index:,composite:queue_created,priority:2"`
	// UpdatedAt 是最近一次状态变化时间：入队、领取、释放、失败、完成、人工重试、改排期时写入，续租不写；
	// 旧行回填前为 NULL。
	UpdatedAt *time.Time `gorm:"column:updated_at;type:timestamp;autoUpdateTime:false;index:,composite:queue_updated,priority:2"`
	// StartedAt 是最近一次领取（开始执行）时间；从未领取为 NULL。
	StartedAt *time.Time `gorm:"column:started_at;type:timestamp"`
}

// managedFields 是 Repo 管理的全部 Model 字段，业务模型不得覆盖或复用这些列。
var managedFields = []string{"Status", "CompletedAt", "ID", "Generation", "Data", "Attempts", "Token", "AvailableAt", "ReservedUntil", "Failed", "FailureReason", "FailedAt", "LastErrorMessage", "LastErrorAt", "MessageVersion", "CreatedAt", "UpdatedAt", "StartedAt"}

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

// timestampFloorPointer 用于记录已发生的时刻（创建、更新、开始执行），向下取整不会把它记得比实际晚。
func timestampFloorPointer(at time.Time, precision time.Duration) *time.Time {
	value := timestampFloor(at, precision)
	return &value
}

// maxMessageVersionBytes 是 message_version 列的长度上限。
const maxMessageVersionBytes = 255

// taskData 只保存不参与筛选、排序或状态变更的消息正文。
type taskData struct {
	MessageVersion string            `json:"message_version"`
	Payload        []byte            `json:"payload,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
}

// corruptRecord 为正文无法解码的失败行构造最小快照；主键或失败时间缺失仍视为状态损坏。
func (m *Model) corruptRecord() (*databasequeue.TaskRecord, error) {
	if strings.TrimSpace(string(m.ID)) == "" || len(m.ID) > 128 || !m.Failed || m.FailedAt == nil {
		return nil, errors.New("decode queue task: invalid stored state")
	}
	record := &databasequeue.TaskRecord{
		Task:     queue.Task{ID: string(m.ID), AvailableAt: m.AvailableAt.UTC()},
		Attempts: m.Attempts, Failed: true, FailureReason: queue.FailureCorruptPayload, FailedAt: m.FailedAt.UTC(),
	}
	m.fillLastError(record)
	return record, nil
}

// fillLastError 把可空的错误列转换为记录中的零值语义。
func (m *Model) fillLastError(record *databasequeue.TaskRecord) {
	if m.LastErrorMessage != nil {
		record.LastError = *m.LastErrorMessage
	}
	if m.LastErrorAt != nil {
		record.LastErrorAt = m.LastErrorAt.UTC()
	}
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
	// 正文保存的是完整精度的创建时间；列只是按存储精度取整的索引副本，正文没有时才用列。
	if task.CreatedAt.IsZero() && m.CreatedAt != nil {
		task.CreatedAt = m.CreatedAt.UTC()
	}
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
	m.fillLastError(record)
	return record, nil
}
