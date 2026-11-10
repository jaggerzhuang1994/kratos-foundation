package gorm

import (
	"context"
	"testing"
	"time"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"gorm.io/gorm"
)

func TestBackfillLegacyRows(t *testing.T) {
	repo, db := testRepo(t)
	runBackfillLegacyRows(t, repo, db)
}

// runBackfillLegacyRows 模拟旧版本写入的行（新列为 NULL），确认回填取值、不覆盖新值、可分批循环到 0。
func runBackfillLegacyRows(t *testing.T, repo *Repo[*testTask], db *gorm.DB) {
	ctx := context.Background()
	created := time.Now().UTC().Truncate(time.Second).Add(-2 * time.Hour)
	failed := created.Add(30 * time.Minute)
	for _, id := range []string{"legacy", "failed", "fresh", "corrupt"} {
		if err := repo.Insert(ctx, &databasequeue.TaskRecord{Task: queue.Task{ID: id, MessageVersion: "v1", AvailableAt: created, CreatedAt: created}}); err != nil {
			t.Fatal(err)
		}
	}
	legacy := map[string]any{"message_version": nil, "created_at": nil, "updated_at": nil}
	if err := db.Model(&testTask{}).Where("id IN ?", []string{"legacy", "failed", "corrupt"}).Updates(legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&testTask{}).Where("id = ?", "failed").Updates(map[string]any{"status": StatusFailed, "failed": true, "failed_at": failed}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&testTask{}).Where("id = ?", "corrupt").Updates(map[string]any{"data": []byte("not json")}).Error; err != nil {
		t.Fatal(err)
	}
	// 回填开始前新版本已经写入了 updated_at：COALESCE 保留这个值。
	kept := created.Add(time.Hour)
	if err := db.Model(&testTask{}).Where("id = ?", "legacy").Update("updated_at", kept).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Backfill(ctx, 0); err == nil {
		t.Fatal("invalid limit accepted")
	}
	total := 0
	for {
		updated, err := repo.Backfill(ctx, 2)
		if err != nil {
			t.Fatal(err)
		}
		if updated == 0 {
			break
		}
		total += updated
	}
	if total != 3 {
		t.Fatalf("backfilled %d rows, want 3", total)
	}
	rows := map[string]testTask{}
	var all []testTask
	if err := db.Find(&all).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range all {
		rows[string(row.ID)] = row
	}
	check := func(id, version string, createdAt, updatedAt time.Time) {
		t.Helper()
		row := rows[id]
		if row.MessageVersion == nil || *row.MessageVersion != version || row.CreatedAt == nil || !row.CreatedAt.Equal(createdAt) || row.UpdatedAt == nil || !row.UpdatedAt.Equal(updatedAt) {
			t.Fatalf("%s = version %v created %v updated %v", id, row.MessageVersion, row.CreatedAt, row.UpdatedAt)
		}
	}
	check("legacy", "v1", created, kept)
	check("failed", "v1", created, failed)
	// 正文损坏：消息版本记空字符串，创建时间退回 available_at。
	check("corrupt", "", created, created)
	if rows["fresh"].UpdatedAt == nil || rows["fresh"].StartedAt != nil {
		t.Fatalf("fresh row changed: %+v", rows["fresh"].Model)
	}
}
