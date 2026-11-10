package queue

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// 查询条件的数量与长度上限：运维查询必须有界，避免一次请求生成超大 SQL 或扫描参数。
const (
	// MaxQueryIDs 是 TaskFilter.IDs 的最大数量。
	MaxQueryIDs = 100
	// MaxQueryValues 是 TaskFilter.MessageVersions、FailureReasons 各自的最大数量。
	MaxQueryValues = 50
	// MaxQueryTextBytes 是 TaskFilter.ErrorContains 的最大字节数。
	MaxQueryTextBytes = 256
	// maxQueryValueBytes 是消息版本、失败分类单个取值的最大字节数。
	maxQueryValueBytes = 255
)

// TimeRange 是左闭右开的时间范围 [From, To)；零值端点表示不限。
type TimeRange struct {
	// From 是包含的起点；零值表示不限。
	From time.Time
	// To 是不包含的终点；零值表示不限。
	To time.Time
}

// IsZero 报告范围两端是否都未设置。
func (r TimeRange) IsZero() bool { return r.From.IsZero() && r.To.IsZero() }

func (r TimeRange) validate(name string) error {
	if !r.From.IsZero() && !r.To.IsZero() && !r.From.Before(r.To) {
		return fmt.Errorf("queue task filter %s range must have from before to", name)
	}
	return nil
}

// TaskFilter 是运维查询、计数与批量操作共用的筛选条件；各字段之间是“且”，同一字段内的多个值是“或”。
// 零值匹配全部任务。时间条件作用于对应的存储列，该列为空（例如从未失败）的任务不匹配设置了范围的条件。
type TaskFilter struct {
	// Statuses 为空时不按状态筛选；状态按查询时刻的租约和排期计算。
	Statuses []TaskStatus
	// IDs 精确匹配任务 ID，最多 MaxQueryIDs 个。
	IDs []string
	// MessageVersions 精确匹配消息契约标识，最多 MaxQueryValues 个。
	MessageVersions []string
	// FailureReasons 精确匹配受控失败分类，最多 MaxQueryValues 个。
	FailureReasons []string
	// CreatedAt 筛选任务创建时间。
	CreatedAt TimeRange
	// UpdatedAt 筛选最近一次状态变化时间。
	UpdatedAt TimeRange
	// AvailableAt 筛选最早可执行时间。
	AvailableAt TimeRange
	// StartedAt 筛选最近一次开始执行（领取）时间。
	StartedAt TimeRange
	// FailedAt 筛选失败归档时间。
	FailedAt TimeRange
	// CompletedAt 筛选成功完成时间。
	CompletedAt TimeRange
	// LastErrorAt 筛选最近一次执行失败时间。
	LastErrorAt TimeRange
	// MinAttempts 大于 0 时只匹配累计领取次数不小于该值的任务。
	MinAttempts int
	// ErrorContains 非空时按子串匹配最近一次执行错误原文（大小写规则取决于数据库排序规则），
	// 最多 MaxQueryTextBytes 字节；该条件无法使用索引，应与其他条件组合缩小范围。
	ErrorContains string
}

// Validate 校验状态取值、数量与长度上限和时间范围。
func (f TaskFilter) Validate() error {
	for _, status := range f.Statuses {
		if !status.Valid() {
			return fmt.Errorf("queue task filter contains invalid status %q", status)
		}
	}
	if len(f.IDs) > MaxQueryIDs {
		return fmt.Errorf("queue task filter accepts at most %d ids", MaxQueryIDs)
	}
	for _, id := range f.IDs {
		if len(id) > 128 || strings.TrimSpace(id) == "" {
			return errors.New("queue task filter contains an invalid id")
		}
	}
	if len(f.MessageVersions) > MaxQueryValues || len(f.FailureReasons) > MaxQueryValues {
		return fmt.Errorf("queue task filter accepts at most %d values per field", MaxQueryValues)
	}
	for _, values := range [][]string{f.MessageVersions, f.FailureReasons} {
		for _, value := range values {
			if strings.TrimSpace(value) == "" || len(value) > maxQueryValueBytes {
				return errors.New("queue task filter contains an invalid value")
			}
		}
	}
	if f.MinAttempts < 0 {
		return errors.New("queue task filter min attempts must not be negative")
	}
	if len(f.ErrorContains) > MaxQueryTextBytes {
		return fmt.Errorf("queue task filter error text must not exceed %d bytes", MaxQueryTextBytes)
	}
	for _, item := range []struct {
		name  string
		value TimeRange
	}{
		{"created_at", f.CreatedAt}, {"updated_at", f.UpdatedAt}, {"available_at", f.AvailableAt},
		{"started_at", f.StartedAt}, {"failed_at", f.FailedAt}, {"completed_at", f.CompletedAt},
		{"last_error_at", f.LastErrorAt},
	} {
		if err := item.value.validate(item.name); err != nil {
			return err
		}
	}
	return nil
}

// OnlyStatuses 报告筛选条件是否只包含状态（零值也算），供只支持按状态筛选的后端判断。
func (f TaskFilter) OnlyStatuses() bool {
	return len(f.IDs) == 0 && len(f.MessageVersions) == 0 && len(f.FailureReasons) == 0 &&
		f.CreatedAt.IsZero() && f.UpdatedAt.IsZero() && f.AvailableAt.IsZero() && f.StartedAt.IsZero() &&
		f.FailedAt.IsZero() && f.CompletedAt.IsZero() && f.LastErrorAt.IsZero() && f.MinAttempts == 0 && f.ErrorContains == ""
}

// TaskSortField 是运维列表的排序字段。
type TaskSortField string

const (
	// TaskSortDefault 按存储主键稳定升序，与旧版本 List 一致。
	TaskSortDefault TaskSortField = ""
	// TaskSortCreatedAt 按任务创建时间排序。
	TaskSortCreatedAt TaskSortField = "created_at"
	// TaskSortUpdatedAt 按最近一次状态变化时间排序。
	TaskSortUpdatedAt TaskSortField = "updated_at"
	// TaskSortAvailableAt 按最早可执行时间排序。
	TaskSortAvailableAt TaskSortField = "available_at"
	// TaskSortStartedAt 按最近一次开始执行时间排序。
	TaskSortStartedAt TaskSortField = "started_at"
	// TaskSortFailedAt 按失败归档时间排序。
	TaskSortFailedAt TaskSortField = "failed_at"
	// TaskSortCompletedAt 按成功完成时间排序。
	TaskSortCompletedAt TaskSortField = "completed_at"
	// TaskSortLastErrorAt 按最近一次执行失败时间排序。
	TaskSortLastErrorAt TaskSortField = "last_error_at"
)

// Valid 报告排序字段是否属于公共契约。
func (f TaskSortField) Valid() bool {
	switch f {
	case TaskSortDefault, TaskSortCreatedAt, TaskSortUpdatedAt, TaskSortAvailableAt, TaskSortStartedAt,
		TaskSortFailedAt, TaskSortCompletedAt, TaskSortLastErrorAt:
		return true
	default:
		return false
	}
}

// TaskSort 是运维列表的排序方式；同值按任务 ID 同方向排列，翻页稳定。
// 字段为空（例如从未开始执行）的任务：升序排在最前，降序排在最后。
type TaskSort struct {
	// Field 为空时按存储主键升序，Desc 必须为 false。
	Field TaskSortField
	// Desc 为 true 时降序。
	Desc bool
}

// TaskQuery 定义有界的任务元数据分页查询；Cursor 由后端生成且不得解析，只能配合生成它的同一 Sort 使用。
type TaskQuery struct {
	// Statuses 为空时查询后端支持的全部状态；与 Filter.Statuses 二选一。
	Statuses []TaskStatus
	// Filter 是附加筛选条件；不支持的后端返回 ErrUnsupported。
	Filter TaskFilter
	// Sort 是排序方式；零值按存储主键升序。不支持的后端返回 ErrUnsupported。
	Sort TaskSort
	// Cursor 是上一次 List 返回的后端不透明游标。
	Cursor string
	// Limit 限制本页返回数量，范围为 1–1000。
	Limit int
}

// Validate 校验分页上限、状态、筛选与排序条件。
func (q TaskQuery) Validate() error {
	if q.Limit < 1 || q.Limit > 1000 {
		return errors.New("queue task query limit must be between 1 and 1000")
	}
	for _, status := range q.Statuses {
		if !status.Valid() {
			return fmt.Errorf("queue task query contains invalid status %q", status)
		}
	}
	if len(q.Statuses) > 0 && len(q.Filter.Statuses) > 0 {
		return errors.New("queue task query statuses and filter statuses are mutually exclusive")
	}
	if err := q.Filter.Validate(); err != nil {
		return err
	}
	if !q.Sort.Field.Valid() {
		return fmt.Errorf("queue task query contains invalid sort field %q", q.Sort.Field)
	}
	if q.Sort.Field == TaskSortDefault && q.Sort.Desc {
		return errors.New("queue task query default sort cannot be descending")
	}
	return nil
}

// EffectiveFilter 返回合并了 Statuses 的筛选条件，后端只需处理 Filter。
func (q TaskQuery) EffectiveFilter() TaskFilter {
	filter := q.Filter
	if len(q.Statuses) > 0 {
		filter.Statuses = q.Statuses
	}
	return filter
}
