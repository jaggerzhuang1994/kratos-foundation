package gorm

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"gorm.io/gorm"
)

// sortColumns 把公共排序字段映射到存储列；同值按 id 同方向排序。
var sortColumns = map[queue.TaskSortField]string{
	queue.TaskSortCreatedAt:   "created_at",
	queue.TaskSortUpdatedAt:   "updated_at",
	queue.TaskSortAvailableAt: "available_at",
	queue.TaskSortStartedAt:   "started_at",
	queue.TaskSortFailedAt:    "failed_at",
	queue.TaskSortCompletedAt: "completed_at",
	queue.TaskSortLastErrorAt: "last_error_at",
}

// applyFilter 把筛选条件转换为参数化 SQL；状态按 now 计算，时间范围按存储精度向上取整两端。
func (r *Repo[T]) applyFilter(db *gorm.DB, filter queue.TaskFilter, now time.Time) *gorm.DB {
	if len(filter.Statuses) > 0 {
		condition, args := operationsStatusCondition(filter.Statuses, now, r.timestampPrecision)
		db = db.Where(condition, args...)
	}
	if len(filter.IDs) > 0 {
		db = db.Where("id IN ?", filter.IDs)
	}
	if len(filter.MessageVersions) > 0 {
		db = db.Where("message_version IN ?", filter.MessageVersions)
	}
	if len(filter.FailureReasons) > 0 {
		db = db.Where("failure_reason IN ?", filter.FailureReasons)
	}
	for _, item := range []struct {
		column string
		value  queue.TimeRange
	}{
		{"created_at", filter.CreatedAt}, {"updated_at", filter.UpdatedAt}, {"available_at", filter.AvailableAt},
		{"started_at", filter.StartedAt}, {"failed_at", filter.FailedAt}, {"completed_at", filter.CompletedAt},
		{"last_error_at", filter.LastErrorAt},
	} {
		if !item.value.From.IsZero() {
			db = db.Where(item.column+" >= ?", timestampCeil(item.value.From, r.timestampPrecision))
		}
		if !item.value.To.IsZero() {
			db = db.Where(item.column+" < ?", timestampCeil(item.value.To, r.timestampPrecision))
		}
	}
	if filter.MinAttempts > 0 {
		db = db.Where("attempts >= ?", filter.MinAttempts)
	}
	if filter.ErrorContains != "" {
		db = db.Where("last_error_message LIKE ? ESCAPE '!'", "%"+escapeLike(filter.ErrorContains)+"%")
	}
	return db
}

// escapeLike 转义 LIKE 通配符，使输入按字面子串匹配。转义符用 !：反斜杠在 MySQL 字符串字面量里本身是转义符，
// 写进 ESCAPE 子句无法同时兼容 MySQL 与 SQLite。
func escapeLike(value string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(value)
}

// listCursor 是排序列表的不透明游标；绑定生成它的排序方式，换排序后不能复用。
type listCursor struct {
	// Field 与 Desc 记录生成游标时的排序。
	Field queue.TaskSortField `json:"f"`
	Desc  bool                `json:"d,omitempty"`
	// Null 表示上一页最后一行的排序列为 NULL。
	Null bool `json:"n,omitempty"`
	// At 是上一页最后一行排序列的 Unix 纳秒。
	At int64 `json:"t,omitempty"`
	// ID 是上一页最后一行的任务 ID。
	ID []byte `json:"i"`
}

const listCursorPrefix = "s1."

func encodeListCursor(cursor listCursor) string {
	raw, _ := json.Marshal(cursor)
	return listCursorPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

func decodeListCursor(value string, sort queue.TaskSort) (listCursor, error) {
	invalid := errors.New("invalid queue task cursor")
	if !strings.HasPrefix(value, listCursorPrefix) {
		return listCursor{}, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, listCursorPrefix))
	if err != nil {
		return listCursor{}, invalid
	}
	var cursor listCursor
	if json.Unmarshal(raw, &cursor) != nil || len(cursor.ID) == 0 || cursor.Field != sort.Field || cursor.Desc != sort.Desc {
		return listCursor{}, invalid
	}
	return cursor, nil
}

// sortValue 读取一行的排序列，NULL 返回 nil。
func (m *Model) sortValue(field queue.TaskSortField) *time.Time {
	switch field {
	case queue.TaskSortCreatedAt:
		return m.CreatedAt
	case queue.TaskSortUpdatedAt:
		return m.UpdatedAt
	case queue.TaskSortAvailableAt:
		value := m.AvailableAt
		return &value
	case queue.TaskSortStartedAt:
		return m.StartedAt
	case queue.TaskSortFailedAt:
		return m.FailedAt
	case queue.TaskSortCompletedAt:
		return m.CompletedAt
	case queue.TaskSortLastErrorAt:
		return m.LastErrorAt
	default:
		return nil
	}
}

func cursorAfter(m *Model, sort queue.TaskSort) listCursor {
	cursor := listCursor{Field: sort.Field, Desc: sort.Desc, ID: []byte(m.ID)}
	if value := m.sortValue(sort.Field); value != nil {
		cursor.At = value.UTC().UnixNano()
	} else {
		cursor.Null = true
	}
	return cursor
}

// applySort 加上排序与键集翻页条件。数据库对 NULL 的默认顺序（升序在前、降序在后）在 MySQL 与 SQLite 一致，
// 游标条件按同样的顺序衔接，NULL 行不会被跳过或重复。
func applySort(db *gorm.DB, sort queue.TaskSort, cursor *listCursor) *gorm.DB {
	column := sortColumns[sort.Field]
	direction, compare := "ASC", ">"
	if sort.Desc {
		direction, compare = "DESC", "<"
	}
	if cursor != nil {
		id := taskID(cursor.ID)
		switch {
		case cursor.Null && sort.Desc:
			db = db.Where(column+" IS NULL AND id < ?", id)
		case cursor.Null:
			db = db.Where("(("+column+" IS NULL AND id > ?) OR "+column+" IS NOT NULL)", id)
		default:
			at := time.Unix(0, cursor.At).UTC()
			condition := "(" + column + " " + compare + " ? OR (" + column + " = ? AND id " + compare + " ?))"
			if sort.Desc {
				condition = "(" + condition + " OR " + column + " IS NULL)"
			}
			db = db.Where(condition, at, at, id)
		}
	}
	return db.Order(column + " " + direction + ", id " + direction)
}
