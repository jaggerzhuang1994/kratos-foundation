package gorm

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"gorm.io/gorm"
)

// queryFixture 写入状态与时间各不相同的任务，并直接设定时间列，使排序和筛选结果可精确断言。
// 时间都落在整秒上，同一套断言也能在秒级精度的 MySQL 上运行。
func queryFixture(t *testing.T, repo *Repo[*testTask], db *gorm.DB) time.Time {
	t.Helper()
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second).Add(-24 * time.Hour)
	at := func(minutes int) *time.Time { value := base.Add(time.Duration(minutes) * time.Minute); return &value }
	rows := []struct {
		id      string
		version string
		values  map[string]any
	}{
		{"a", "v1", map[string]any{"created_at": at(1), "updated_at": at(10), "started_at": nil}},
		{"b", "v1", map[string]any{"created_at": at(2), "updated_at": at(30), "started_at": at(20), "status": StatusFailed, "failed": true, "failed_at": at(30), "failure_reason": "retry_exhausted", "attempts": 3, "last_error_message": "boom 100% down", "last_error_at": at(30)}},
		{"c", "v2", map[string]any{"created_at": at(3), "updated_at": at(25), "started_at": at(15), "status": StatusCompleted, "completed_at": at(25), "attempts": 1}},
		{"d", "v2", map[string]any{"created_at": at(3), "updated_at": at(5), "started_at": nil, "available_at": time.Now().UTC().Add(time.Hour).Truncate(time.Second)}},
		{"e", "v1", map[string]any{"created_at": at(4), "updated_at": at(40), "started_at": at(35), "status": StatusFailed, "failed": true, "failed_at": at(40), "failure_reason": "permanent", "attempts": 1, "last_error_message": "timeout", "last_error_at": at(40)}},
		{"f", "v1", map[string]any{"created_at": nil, "updated_at": nil, "started_at": nil, "message_version": nil}},
	}
	for _, row := range rows {
		if err := repo.Insert(ctx, &databasequeue.TaskRecord{Task: queue.Task{ID: row.id, MessageVersion: row.version, AvailableAt: base, CreatedAt: base}}); err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&testTask{}).Where("id = ?", row.id).Updates(row.values).Error; err != nil {
			t.Fatal(err)
		}
	}
	return base
}

func listIDs(t *testing.T, repo *Repo[*testTask], query queue.TaskQuery) []string {
	t.Helper()
	var ids []string
	for {
		page, err := repo.ListTasks(context.Background(), query)
		if err != nil {
			t.Fatalf("ListTasks(%+v): %v", query, err)
		}
		for _, task := range page.Tasks {
			ids = append(ids, task.ID)
		}
		if page.NextCursor == "" {
			return ids
		}
		query.Cursor = page.NextCursor
	}
}

func TestListTasksFiltersAndSorts(t *testing.T) {
	repo, db := testRepo(t)
	runListTasksFiltersAndSorts(t, repo, db)
}

func runListTasksFiltersAndSorts(t *testing.T, repo *Repo[*testTask], db *gorm.DB) {
	base := queryFixture(t, repo, db)
	minute := func(n int) time.Time { return base.Add(time.Duration(n) * time.Minute) }
	filters := []struct {
		name   string
		filter queue.TaskFilter
		want   []string
	}{
		{"status", queue.TaskFilter{Statuses: []queue.TaskStatus{queue.TaskStatusFailed}}, []string{"b", "e"}},
		{"scheduled", queue.TaskFilter{Statuses: []queue.TaskStatus{queue.TaskStatusScheduled}}, []string{"d"}},
		{"ids", queue.TaskFilter{IDs: []string{"c", "f", "missing"}}, []string{"c", "f"}},
		{"version", queue.TaskFilter{MessageVersions: []string{"v2"}}, []string{"c", "d"}},
		{"reason", queue.TaskFilter{FailureReasons: []string{"permanent"}}, []string{"e"}},
		{"created range", queue.TaskFilter{CreatedAt: queue.TimeRange{From: minute(2), To: minute(4)}}, []string{"b", "c", "d"}},
		{"updated from", queue.TaskFilter{UpdatedAt: queue.TimeRange{From: minute(30)}}, []string{"b", "e"}},
		{"started to", queue.TaskFilter{StartedAt: queue.TimeRange{To: minute(20)}}, []string{"c"}},
		{"failed", queue.TaskFilter{FailedAt: queue.TimeRange{From: minute(31)}}, []string{"e"}},
		{"completed", queue.TaskFilter{CompletedAt: queue.TimeRange{From: minute(1)}}, []string{"c"}},
		{"last error", queue.TaskFilter{LastErrorAt: queue.TimeRange{To: minute(31)}}, []string{"b"}},
		{"available", queue.TaskFilter{AvailableAt: queue.TimeRange{From: time.Now()}}, []string{"d"}},
		{"attempts", queue.TaskFilter{MinAttempts: 2}, []string{"b"}},
		// % 按字面匹配，不是通配符。
		{"error literal percent", queue.TaskFilter{ErrorContains: "100%"}, []string{"b"}},
		{"error wildcard escaped", queue.TaskFilter{ErrorContains: "t_me"}, nil},
		{"error escape char", queue.TaskFilter{ErrorContains: "!"}, nil},
		{"combined", queue.TaskFilter{MessageVersions: []string{"v1"}, Statuses: []queue.TaskStatus{queue.TaskStatusFailed}, MinAttempts: 1}, []string{"b", "e"}},
	}
	for _, test := range filters {
		t.Run("filter "+test.name, func(t *testing.T) {
			got := listIDs(t, repo, queue.TaskQuery{Limit: 2, Filter: test.filter})
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ids = %v, want %v", got, test.want)
			}
		})
	}
	// 同值按 ID 同方向排；NULL 升序在前、降序在后；每页 2 条翻页不丢不重。
	sorts := []struct {
		sort queue.TaskSort
		want []string
	}{
		{queue.TaskSort{Field: queue.TaskSortCreatedAt}, []string{"f", "a", "b", "c", "d", "e"}},
		{queue.TaskSort{Field: queue.TaskSortCreatedAt, Desc: true}, []string{"e", "d", "c", "b", "a", "f"}},
		{queue.TaskSort{Field: queue.TaskSortUpdatedAt, Desc: true}, []string{"e", "b", "c", "a", "d", "f"}},
		{queue.TaskSort{Field: queue.TaskSortStartedAt}, []string{"a", "d", "f", "c", "b", "e"}},
		{queue.TaskSort{Field: queue.TaskSortStartedAt, Desc: true}, []string{"e", "b", "c", "f", "d", "a"}},
		{queue.TaskSort{Field: queue.TaskSortFailedAt, Desc: true}, []string{"e", "b", "f", "d", "c", "a"}},
		{queue.TaskSort{Field: queue.TaskSortCompletedAt, Desc: true}, []string{"c", "f", "e", "d", "b", "a"}},
		{queue.TaskSort{Field: queue.TaskSortLastErrorAt}, []string{"a", "c", "d", "f", "b", "e"}},
		{queue.TaskSort{Field: queue.TaskSortAvailableAt, Desc: true}, []string{"d", "f", "e", "c", "b", "a"}},
	}
	for _, test := range sorts {
		name := string(test.sort.Field)
		if test.sort.Desc {
			name += " desc"
		}
		t.Run("sort "+name, func(t *testing.T) {
			got := listIDs(t, repo, queue.TaskQuery{Limit: 2, Sort: test.sort})
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ids = %v, want %v", got, test.want)
			}
		})
	}
	t.Run("sort with filter", func(t *testing.T) {
		got := listIDs(t, repo, queue.TaskQuery{Limit: 1, Filter: queue.TaskFilter{Statuses: []queue.TaskStatus{queue.TaskStatusFailed}}, Sort: queue.TaskSort{Field: queue.TaskSortCreatedAt, Desc: true}})
		if !reflect.DeepEqual(got, []string{"e", "b"}) {
			t.Fatalf("ids = %v", got)
		}
	})
}

func TestListTasksCursorBoundToSort(t *testing.T) {
	repo, db := testRepo(t)
	queryFixture(t, repo, db)
	ctx := context.Background()
	created := queue.TaskSort{Field: queue.TaskSortCreatedAt}
	page, err := repo.ListTasks(ctx, queue.TaskQuery{Limit: 1, Sort: created})
	if err != nil || page.NextCursor == "" || !strings.HasPrefix(page.NextCursor, listCursorPrefix) {
		t.Fatalf("first page = %#v, %v", page, err)
	}
	for _, query := range []queue.TaskQuery{
		{Limit: 1, Cursor: page.NextCursor, Sort: queue.TaskSort{Field: queue.TaskSortUpdatedAt}},
		{Limit: 1, Cursor: page.NextCursor, Sort: queue.TaskSort{Field: queue.TaskSortCreatedAt, Desc: true}},
		{Limit: 1, Cursor: page.NextCursor},
		{Limit: 1, Cursor: "s1.%%%", Sort: created},
		{Limit: 1, Cursor: "plain", Sort: created},
		{Limit: 1, Cursor: encodeListCursor(listCursor{Field: queue.TaskSortCreatedAt}), Sort: created},
	} {
		if _, err := repo.ListTasks(ctx, query); err == nil || !strings.Contains(err.Error(), "invalid queue task cursor") {
			t.Fatalf("cursor %q with sort %+v: %v", query.Cursor, query.Sort, err)
		}
	}
	// 默认排序仍使用旧格式游标，升级前后保存的分页位置都能继续用。
	legacy, err := repo.ListTasks(ctx, queue.TaskQuery{Limit: 1})
	if err != nil || legacy.NextCursor != encodeOperationsCursor("a") {
		t.Fatalf("legacy cursor = %#v, %v", legacy, err)
	}
	if _, err := repo.ListTasks(ctx, queue.TaskQuery{Limit: 1, Sort: queue.TaskSort{Field: "payload"}}); err == nil {
		t.Fatal("invalid sort accepted")
	}
}

func TestSummaryIncludesNewTimestamps(t *testing.T) {
	repo, db := testRepo(t)
	base := queryFixture(t, repo, db)
	detail, err := repo.GetTask(context.Background(), "b")
	if err != nil {
		t.Fatal(err)
	}
	summary := detail.Summary
	if !summary.UpdatedAt.Equal(base.Add(30*time.Minute)) || !summary.StartedAt.Equal(base.Add(20*time.Minute)) {
		t.Fatalf("summary = %+v", summary)
	}
	// 正文里有完整精度的创建时间时以正文为准；列只是排序和筛选用的副本。
	if !summary.CreatedAt.Equal(base) {
		t.Fatalf("created_at = %s, want body value %s", summary.CreatedAt, base)
	}
	legacy, err := repo.GetTask(context.Background(), "f")
	if err != nil || !legacy.Summary.UpdatedAt.IsZero() || !legacy.Summary.StartedAt.IsZero() || legacy.Summary.CreatedAt.IsZero() {
		t.Fatalf("legacy summary = %+v, %v", legacy.Summary, err)
	}
}
