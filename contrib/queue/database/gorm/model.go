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

// Model 是队列管理的存储字段。业务须按值匿名嵌入，不得覆盖字段、列名或添加软删除。
// 不定义 TableName；表名由业务模型提供。ID 是任务 ID 的十六进制编码，避免数据库
// 大小写、音调和尾随空格排序规则改变任务唯一性。Identity 隔离删除后重新插入的同 ID。
type Model struct {
	ID            string `gorm:"column:id;primaryKey;size:256"`
	Identity      string `gorm:"column:identity;size:36;not null"`
	Data          []byte `gorm:"column:data;not null"`
	Attempts      int    `gorm:"column:attempts;not null"`
	Token         string `gorm:"column:token;size:36;not null"`
	AvailableAt   int64  `gorm:"column:available_at;not null;index"`
	ReservedUntil int64  `gorm:"column:reserved_until;not null"`
	Failed        bool   `gorm:"column:failed;not null;index"`
	FailureReason string `gorm:"column:failure_reason;size:128;not null"`
	FailedAt      int64  `gorm:"column:failed_at;not null"`
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
