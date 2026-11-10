package queue

import (
	"strings"
	"testing"
	"time"
)

func TestTaskQueryValidateRejectsInvalidInput(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name  string
		query TaskQuery
	}{
		{name: "zero limit", query: TaskQuery{}},
		{name: "limit too large", query: TaskQuery{Limit: 1001}},
		{name: "unknown status", query: TaskQuery{Limit: 1, Statuses: []TaskStatus{"unknown"}}},
		{name: "unknown filter status", query: TaskQuery{Limit: 1, Filter: TaskFilter{Statuses: []TaskStatus{"unknown"}}}},
		{name: "both status lists", query: TaskQuery{Limit: 1, Statuses: []TaskStatus{TaskStatusFailed}, Filter: TaskFilter{Statuses: []TaskStatus{TaskStatusFailed}}}},
		{name: "unknown sort", query: TaskQuery{Limit: 1, Sort: TaskSort{Field: "payload"}}},
		{name: "descending default sort", query: TaskQuery{Limit: 1, Sort: TaskSort{Desc: true}}},
		{name: "too many ids", query: TaskQuery{Limit: 1, Filter: TaskFilter{IDs: make([]string, MaxQueryIDs+1)}}},
		{name: "blank id", query: TaskQuery{Limit: 1, Filter: TaskFilter{IDs: []string{" "}}}},
		{name: "long id", query: TaskQuery{Limit: 1, Filter: TaskFilter{IDs: []string{strings.Repeat("x", 129)}}}},
		{name: "too many versions", query: TaskQuery{Limit: 1, Filter: TaskFilter{MessageVersions: make([]string, MaxQueryValues+1)}}},
		{name: "blank reason", query: TaskQuery{Limit: 1, Filter: TaskFilter{FailureReasons: []string{""}}}},
		{name: "long version", query: TaskQuery{Limit: 1, Filter: TaskFilter{MessageVersions: []string{strings.Repeat("v", 256)}}}},
		{name: "negative attempts", query: TaskQuery{Limit: 1, Filter: TaskFilter{MinAttempts: -1}}},
		{name: "long error text", query: TaskQuery{Limit: 1, Filter: TaskFilter{ErrorContains: strings.Repeat("e", MaxQueryTextBytes+1)}}},
		{name: "empty range", query: TaskQuery{Limit: 1, Filter: TaskFilter{CreatedAt: TimeRange{From: now, To: now}}}},
		{name: "reversed range", query: TaskQuery{Limit: 1, Filter: TaskFilter{LastErrorAt: TimeRange{From: now, To: now.Add(-time.Second)}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.query.Validate(); err == nil {
				t.Fatalf("Validate() error = nil for %#v", test.query)
			}
		})
	}
}

func TestTaskQueryValidateAcceptsFullQuery(t *testing.T) {
	now := time.Now()
	query := TaskQuery{
		Limit: 1000,
		Filter: TaskFilter{
			Statuses: []TaskStatus{TaskStatusFailed}, IDs: []string{"a"}, MessageVersions: []string{"v1"},
			FailureReasons: []string{FailureCorruptPayload}, MinAttempts: 3, ErrorContains: strings.Repeat("e", MaxQueryTextBytes),
			CreatedAt: TimeRange{From: now.Add(-time.Hour), To: now}, UpdatedAt: TimeRange{From: now}, FailedAt: TimeRange{To: now},
		},
		Sort: TaskSort{Field: TaskSortCreatedAt, Desc: true},
	}
	if err := query.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, field := range []TaskSortField{TaskSortDefault, TaskSortCreatedAt, TaskSortUpdatedAt, TaskSortAvailableAt, TaskSortStartedAt, TaskSortFailedAt, TaskSortCompletedAt, TaskSortLastErrorAt} {
		if !field.Valid() {
			t.Fatalf("%q should be valid", field)
		}
	}
}

func TestTaskQueryEffectiveFilterMergesLegacyStatuses(t *testing.T) {
	legacy := TaskQuery{Statuses: []TaskStatus{TaskStatusFailed}, Filter: TaskFilter{MinAttempts: 2}}
	filter := legacy.EffectiveFilter()
	if len(filter.Statuses) != 1 || filter.Statuses[0] != TaskStatusFailed || filter.MinAttempts != 2 {
		t.Fatalf("EffectiveFilter() = %#v", filter)
	}
	modern := TaskQuery{Filter: TaskFilter{Statuses: []TaskStatus{TaskStatusPending}}}
	if got := modern.EffectiveFilter().Statuses; len(got) != 1 || got[0] != TaskStatusPending {
		t.Fatalf("EffectiveFilter() statuses = %v", got)
	}
}

func TestTaskFilterOnlyStatuses(t *testing.T) {
	if !(TaskFilter{}).OnlyStatuses() || !(TaskFilter{Statuses: []TaskStatus{TaskStatusFailed}}).OnlyStatuses() {
		t.Fatal("status-only filters should report true")
	}
	for _, filter := range []TaskFilter{
		{IDs: []string{"a"}}, {MessageVersions: []string{"v"}}, {FailureReasons: []string{"r"}}, {MinAttempts: 1},
		{ErrorContains: "x"}, {CreatedAt: TimeRange{From: time.Now()}}, {CompletedAt: TimeRange{To: time.Now()}},
	} {
		if filter.OnlyStatuses() {
			t.Fatalf("%#v should not be status-only", filter)
		}
	}
	if !(TimeRange{}).IsZero() || (TimeRange{To: time.Now()}).IsZero() {
		t.Fatal("TimeRange.IsZero mismatch")
	}
}
