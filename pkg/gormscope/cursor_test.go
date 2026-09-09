package gormscope

import (
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestGetSortKeySupportsQualifiedColumns(t *testing.T) {
	t.Parallel()

	type user struct {
		ID uint64
	}
	keys := SortKeys{NumberAscSortKey("users.id")}

	got, err := keys.GetSortKey(user{ID: 42}, ":")
	if err != nil {
		t.Fatal(err)
	}
	if got != "42" {
		t.Fatalf("GetSortKey() = %q, want %q", got, "42")
	}
}

func TestGetSortKeySupportsTaggedColumnNames(t *testing.T) {
	t.Parallel()

	type user struct {
		Identifier uint64 `gorm:"column:custom_id"`
	}
	keys := SortKeys{NumberAscSortKey("users.custom_id")}

	got, err := keys.GetSortKey(user{Identifier: 7}, ":")
	if err != nil {
		t.Fatal(err)
	}
	if got != "7" {
		t.Fatalf("GetSortKey() = %q, want %q", got, "7")
	}
}

func TestCursorPaginationHandlesMixedDirections(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	if err := db.AutoMigrate(&scopeRecord{}); err != nil {
		t.Fatal(err)
	}
	records := []scopeRecord{
		{ID: 1, Name: "one", Score: 20},
		{ID: 2, Name: "two", Score: 20},
		{ID: 3, Name: "three", Score: 10},
		{ID: 4, Name: "four", Score: 10},
	}
	if err := db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}
	keys := SortKeys{NumberDescSortKey("score"), NumberAscSortKey("id")}

	var first []scopeRecord
	if err := db.Scopes(ScopeFromSortKey(nil, keys, ":")).Limit(2).Find(&first).Error; err != nil {
		t.Fatal(err)
	}
	if ids := recordIDs(first); len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("first cursor page IDs = %v, want [1 2]", ids)
	}
	cursor, err := keys.GetSortKey(first[len(first)-1], ":")
	if err != nil {
		t.Fatal(err)
	}

	var second []scopeRecord
	if err = db.Scopes(ScopeFromSortKey(&cursor, keys, ":")).Limit(2).Find(&second).Error; err != nil {
		t.Fatal(err)
	}
	if ids := recordIDs(second); len(ids) != 2 || ids[0] != 3 || ids[1] != 4 {
		t.Fatalf("second cursor page IDs = %v, want [3 4]", ids)
	}
}

func TestCursorEncodesTimeNumberAndStringValues(t *testing.T) {
	t.Parallel()

	type cursorModel struct {
		CreatedAt time.Time
		Amount    float64
		Name      string
	}
	createdAt := time.Unix(1700000000, 123456789).UTC()
	keys := SortKeys{
		TimeAscSortKey("created_at"),
		NumberDescSortKey("amount"),
		StringAscSortKey("name"),
	}
	got, err := keys.GetSortKey(cursorModel{CreatedAt: createdAt, Amount: 12.5, Name: "alice"}, "|")
	if err != nil {
		t.Fatal(err)
	}
	if got != "1700000000123456789|12.5|alice" {
		t.Fatalf("GetSortKey() = %q", got)
	}
	if _, err = keys.GetSortKey(cursorModel{Name: "a|b"}, "|"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("GetSortKey() separator error = %v, want %v", err, ErrInvalidCursor)
	}
}

func TestGetSortKeyRejectsInvalidModelsFieldsAndValues(t *testing.T) {
	t.Parallel()

	if _, err := (SortKeys{NumberAscSortKey("id")}).GetSortKey(nil, ":"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("nil model error = %v, want ErrInvalidCursor", err)
	}
	type model struct {
		ID   uint64
		When *time.Time
		Name string
	}
	if _, err := (SortKeys{NumberAscSortKey("missing")}).GetSortKey(model{}, ":"); !errors.Is(err, ErrInvalidCursor) || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing field error = %v", err)
	}
	if _, err := (SortKeys{TimeAscSortKey("when")}).GetSortKey(model{}, ":"); !errors.Is(err, ErrInvalidCursor) || !strings.Contains(err.Error(), "encode") {
		t.Fatalf("nil time field error = %v", err)
	}
	if _, err := (SortKeys{NumberAscSortKey("name")}).GetSortKey(model{Name: "not-a-number"}, ":"); !errors.Is(err, ErrInvalidCursor) || !strings.Contains(err.Error(), "encode") {
		t.Fatalf("invalid number field error = %v", err)
	}
	if _, err := (SortKeys{NumberAscSortKey("id")}).GetSortKey(make(chan int), ":"); err == nil || !strings.Contains(err.Error(), "parse cursor model") {
		t.Fatalf("unparseable model error = %v", err)
	}
}

func TestCursorScopesRejectKeyShapeValueCountAndDecodeErrors(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	invalidKeys := []SortKeys{
		{nil},
		{NumberAscSortKey("")},
	}
	for _, keys := range invalidKeys {
		result := ScopeFromSortKey(nil, keys, ":")(db.Session(&gorm.Session{DryRun: true}))
		if !errors.Is(result.Error, ErrInvalidCursor) {
			t.Errorf("invalid keys %v error = %v, want ErrInvalidCursor", keys, result.Error)
		}
	}

	wrongCount := "1"
	result := ScopeFromSortKey(&wrongCount, SortKeys{NumberAscSortKey("score"), NumberAscSortKey("id")}, ":")(db.Session(&gorm.Session{DryRun: true}))
	if !errors.Is(result.Error, ErrInvalidCursor) || !strings.Contains(result.Error.Error(), "expected 2 values") {
		t.Fatalf("value-count error = %v", result.Error)
	}
	for _, test := range []struct {
		name   string
		cursor string
		key    SortKey
	}{
		{name: "time", cursor: "not-a-time", key: TimeAscSortKey("created_at")},
		{name: "number", cursor: "NaN", key: NumberAscSortKey("score")},
		{name: "unknown kind", cursor: "value", key: sortKey{name: "score", direction: true, kind: sortKind(255)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := ScopeFromSortKey(&test.cursor, SortKeys{test.key}, ":")(db.Session(&gorm.Session{DryRun: true}))
			if !errors.Is(result.Error, ErrInvalidCursor) || !strings.Contains(result.Error.Error(), "decode") {
				t.Fatalf("decode error = %v, want wrapped ErrInvalidCursor", result.Error)
			}
		})
	}
	recursiveDecode := "1:NaN"
	result = ScopeFromSortKey(&recursiveDecode, SortKeys{NumberAscSortKey("id"), NumberAscSortKey("score")}, ":")(db.Session(&gorm.Session{DryRun: true}))
	if !errors.Is(result.Error, ErrInvalidCursor) || !strings.Contains(result.Error.Error(), `decode "score"`) {
		t.Fatalf("recursive decode error = %v", result.Error)
	}
	invalidQuotedField := "1"
	result = ScopeFromSortKey(&invalidQuotedField, SortKeys{NumberAscSortKey("users.id.extra")}, ":")(db.Session(&gorm.Session{DryRun: true}))
	if result.Error == nil || !strings.Contains(result.Error.Error(), "invalid GORM identifier") {
		t.Fatalf("cursor identifier error = %v", result.Error)
	}
	for _, field := range []string{".id", "users.", "users.id.extra"} {
		result := ScopeFromSortKey(nil, SortKeys{NumberAscSortKey(field)}, ":")(db.Session(&gorm.Session{DryRun: true}))
		if result.Error == nil || !strings.Contains(result.Error.Error(), "invalid GORM identifier") {
			t.Errorf("field %q error = %v", field, result.Error)
		}
	}
}
