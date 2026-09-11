package gorm

import (
	"context"
	"errors"
	"testing"
	"time"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

func TestStats(t *testing.T) {
	repo, _ := testRepo(t)
	ctx := context.Background()
	now := time.Unix(1800000000, 0).UTC()
	empty, err := repo.Stats(ctx, now)
	if err != nil || empty.Ready != 0 || !empty.OldestReadyKnown {
		t.Fatalf("empty: %+v, %v", empty, err)
	}
	for _, item := range []struct {
		name      string
		at, until time.Time
		failed    bool
	}{
		{"ready", now.Add(-time.Minute), time.Time{}, false},
		{"expired", now.Add(-2 * time.Minute), now, false},
		{"running", now.Add(-3 * time.Minute), now.Add(time.Second), false},
		{"scheduled", now.Add(time.Second), time.Time{}, false},
		{"failed", now.Add(-4 * time.Minute), time.Time{}, true},
	} {
		err := repo.Insert(ctx, &databasequeue.TaskRecord{Task: queue.Task{ID: item.name, Type: "job", AvailableAt: item.at}, ReservedUntil: item.until, Failed: item.failed})
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.Stats(ctx, now)
	if err != nil || got.Ready != 2 || got.Scheduled != 1 || got.Running != 1 || got.Failed != 1 || !got.OldestReadyAt.Equal(now.Add(-2*time.Minute)) {
		t.Fatalf("stats: %+v, %v", got, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repo.Stats(ctx, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
