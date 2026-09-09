package gormscope

import (
	"math"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type scopeRecord struct {
	ID        uint64 `gorm:"primaryKey"`
	Name      string
	Score     int64
	CreatedAt time.Time
}

func openTestDB(t testing.TB) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestPaginateScopeCanBeReusedConcurrently(t *testing.T) {
	db := openTestDB(t)
	scope := Paginate(2, 100, 10)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result := scope(db.Session(&gorm.Session{DryRun: true}))
			if result.Error != nil {
				t.Errorf("Paginate() error = %v", result.Error)
			}
		}()
	}
	close(start)
	wait.Wait()
}

func TestFilteringScopesUseEscapedLiteralsAndInclusiveTimeBounds(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	if err := db.AutoMigrate(&scopeRecord{}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	records := []scopeRecord{
		{ID: 1, Name: "alpha_100%", CreatedAt: base},
		{ID: 2, Name: "alpha-other", CreatedAt: base.Add(time.Hour)},
		{ID: 3, Name: "other-alpha", CreatedAt: base.Add(2 * time.Hour)},
	}
	if err := db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}

	var got []scopeRecord
	if err := db.Scopes(Contains("name", "_100%"), TimeBetween("created_at", &base, &base)).Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("filtered IDs = %v, want [1]", recordIDs(got))
	}

	got = nil
	if err := db.Scopes(Prefix("name", "alpha")).Order("id").Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	if ids := recordIDs(got); len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("prefix IDs = %v, want [1 2]", ids)
	}

	got = nil
	if err := db.Scopes(Suffix("name", "alpha")).Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != 3 {
		t.Fatalf("suffix IDs = %v, want [3]", recordIDs(got))
	}
}

func TestScopesRejectInvalidArgumentsAndClampPageSize(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	for _, scope := range []Scope{
		Paginate(0, 10, 100),
		Paginate(1, 0, 100),
		ScopeFromSortKey(nil, nil, ":"),
		ScopeFromSortKey(nil, SortKeys{NumberAscSortKey("id")}, ""),
	} {
		result := scope(db.Session(&gorm.Session{DryRun: true}))
		if result.Error == nil {
			t.Errorf("scope %#v accepted invalid arguments", scope)
		}
	}

	if err := db.AutoMigrate(&scopeRecord{}); err != nil {
		t.Fatal(err)
	}
	records := make([]scopeRecord, 25)
	for index := range records {
		records[index].ID = uint64(index + 1)
	}
	if err := db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}
	var page []scopeRecord
	if err := db.Scopes(Paginate(2, 100, 10)).Order("id").Find(&page).Error; err != nil {
		t.Fatal(err)
	}
	if len(page) != 10 || page[0].ID != 11 || page[9].ID != 20 {
		t.Fatalf("Paginate page IDs = %v, want 11 through 20", recordIDs(page))
	}
}

func TestQuoteIdentifierUsesDialectAndRejectsInvalidNames(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	quoted, err := QuoteIdentifier(db, "users.name")
	if err != nil {
		t.Fatal(err)
	}
	if quoted != "`users`.`name`" {
		t.Fatalf("QuoteIdentifier() = %q, want %q", quoted, "`users`.`name`")
	}
	for _, identifier := range []string{"", ".name", "users.", "main.users.name", "bad\x00name"} {
		if _, err = QuoteIdentifier(db, identifier); err == nil {
			t.Errorf("QuoteIdentifier accepted %q", identifier)
		}
	}
}

func recordIDs(records []scopeRecord) []uint64 {
	ids := make([]uint64, len(records))
	for index := range records {
		ids[index] = records[index].ID
	}
	return ids
}

func TestEscapeLikeEscapesEscapeCharacterBeforeWildcards(t *testing.T) {
	t.Parallel()

	if got := EscapeLike("a!_%b"); got != "a!!!_!%b" {
		t.Fatalf("EscapeLike() = %q, want %q", got, "a!!!_!%b")
	}
}

func TestTimeBetweenHandlesNoBoundsAndEitherSingleBound(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	if err := db.AutoMigrate(&scopeRecord{}); err != nil {
		t.Fatal(err)
	}
	boundary := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	records := []scopeRecord{
		{ID: 1, CreatedAt: boundary.Add(-time.Second)},
		{ID: 2, CreatedAt: boundary},
		{ID: 3, CreatedAt: boundary.Add(time.Second)},
	}
	if err := db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		begin *time.Time
		end   *time.Time
		want  []uint64
	}{
		{name: "no bounds", want: []uint64{1, 2, 3}},
		{name: "begin only", begin: &boundary, want: []uint64{2, 3}},
		{name: "end only", end: &boundary, want: []uint64{1, 2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var got []scopeRecord
			err := db.Scopes(TimeBetween("created_at", test.begin, test.end)).Order("id").Find(&got).Error
			if err != nil {
				t.Fatal(err)
			}
			ids := recordIDs(got)
			if len(ids) != len(test.want) {
				t.Fatalf("IDs = %v, want %v", ids, test.want)
			}
			for index := range ids {
				if ids[index] != test.want[index] {
					t.Fatalf("IDs = %v, want %v", ids, test.want)
				}
			}
		})
	}
}

func TestFilteringScopesReportInvalidColumnsOnlyWhenUsed(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	if result := Like("users.name.extra", "%value%")(db.Session(&gorm.Session{DryRun: true})); result.Error == nil {
		t.Fatal("Like accepted an invalid column")
	}
	now := time.Unix(1700000000, 0).UTC()
	if result := TimeBetween("users.created_at.extra", &now, nil)(db.Session(&gorm.Session{DryRun: true})); result.Error == nil {
		t.Fatal("TimeBetween accepted an invalid column with an active bound")
	}
	if result := TimeBetween("users.created_at.extra", nil, nil)(db.Session(&gorm.Session{DryRun: true})); result.Error != nil {
		t.Fatalf("no-op TimeBetween error = %v", result.Error)
	}
}

func TestPaginateDoesNotClampWhenMaximumIsDisabled(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	if err := db.AutoMigrate(&scopeRecord{}); err != nil {
		t.Fatal(err)
	}
	records := make([]scopeRecord, 5)
	for index := range records {
		records[index].ID = uint64(index + 1)
	}
	if err := db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}
	for _, maximum := range []int{0, -1} {
		var got []scopeRecord
		if err := db.Scopes(Paginate(1, 4, maximum)).Order("id").Find(&got).Error; err != nil {
			t.Fatal(err)
		}
		if ids := recordIDs(got); len(ids) != 4 || ids[0] != 1 || ids[3] != 4 {
			t.Fatalf("maximum %d IDs = %v, want [1 2 3 4]", maximum, ids)
		}
	}
}

func TestQuoteIdentifierRejectsMissingDatabaseComponents(t *testing.T) {
	t.Parallel()

	for _, db := range []*gorm.DB{
		nil,
		{},
		{Config: &gorm.Config{}},
	} {
		if quoted, err := QuoteIdentifier(db, "users.id"); err == nil {
			t.Fatalf("QuoteIdentifier(%#v) = %q, want error", db, quoted)
		}
	}
}

func TestPaginateRejectsOffsetOverflow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                string
		page, size, maximum int
		wantError           bool
	}{
		{"overflow", math.MaxInt, 2, 0, true},
		{"largest valid offset", math.MaxInt, 1, 0, false},
		{"clamped before range check", math.MaxInt, 2, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t).Session(&gorm.Session{DryRun: true})
			var result []scopeRecord
			err := db.Scopes(Paginate(tc.page, tc.size, tc.maximum)).Find(&result).Error
			if (err != nil) != tc.wantError {
				t.Fatalf("Paginate error = %v, wantError = %v", err, tc.wantError)
			}
		})
	}
}
