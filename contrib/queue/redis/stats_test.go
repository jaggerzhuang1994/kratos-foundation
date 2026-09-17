package redis

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	goredis "github.com/redis/go-redis/v9"
)

func TestStats(t *testing.T) {
	now := time.Unix(1800000000, 0).UTC()
	task, err := json.Marshal(&queue.Task{ID: "id", MessageVersion: "job", AvailableAt: now.Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(record{Task: string(task)})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name           string
		values         []any
		failure        error
		ready          int64
		known, wantErr bool
	}{
		{name: "empty", values: []any{int64(0), int64(1), int64(2), int64(3)}, known: true},
		{name: "ready", values: []any{int64(1), int64(1), int64(2), int64(3), string(raw)}, ready: 1, known: true},
		{name: "bounded", values: []any{int64(1001), int64(1), int64(2), int64(3)}, ready: 1001},
		{name: "missing", values: []any{int64(1), int64(1), int64(2), int64(3)}, wantErr: true},
		{name: "corrupt", values: []any{int64(1), int64(1), int64(2), int64(3), "invalid"}, wantErr: true},
		{name: "offline", failure: errors.New("offline"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := goredis.NewClient(&goredis.Options{Addr: "unused", ContextTimeoutEnabled: true})
			t.Cleanup(func() {
				if err := client.Close(); err != nil {
					t.Error(err)
				}
			})
			client.AddHook(commandHook{run: func(cmd goredis.Cmder) error { cmd.(*goredis.Cmd).SetVal(tc.values); return tc.failure }})
			store, err := NewStore(testManager{client: client}, Config{Connection: "test", KeyPrefix: "stats"})
			if err != nil {
				t.Fatal(err)
			}
			got, err := store.Stats(context.Background(), now)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error: %v", err)
			}
			if tc.wantErr {
				return
			}
			if got.Ready != tc.ready || got.Scheduled != 1 || got.Running != 2 || got.Failed != 3 || got.OldestReadyKnown != tc.known {
				t.Fatalf("stats: %+v", got)
			}
			if tc.ready == 1 && !got.OldestReadyAt.Equal(now.Add(-time.Minute)) {
				t.Fatalf("age: %+v", got)
			}
		})
	}
}

func TestStatsRedis(t *testing.T) {
	store, ctx := integrationStore(t)
	now := time.Unix(1800000000, 0).UTC()
	empty, err := store.Stats(ctx, now)
	if err != nil || empty.Ready != 0 || !empty.OldestReadyKnown {
		t.Fatalf("empty: %+v %v", empty, err)
	}
	for _, item := range []struct {
		id               string
		available, clock time.Time
		lease            time.Duration
		fail             bool
	}{
		{"expired", now.Add(-2 * time.Minute), now.Add(-2 * time.Second), time.Second, false},
		{"running", now.Add(-time.Minute), now.Add(-1500 * time.Millisecond), time.Minute, false},
		{"failed", now.Add(-30 * time.Second), now.Add(-1400 * time.Millisecond), time.Minute, true},
	} {
		if err := store.Enqueue(ctx, &queue.Task{ID: item.id, MessageVersion: "job", AvailableAt: item.available}); err != nil {
			t.Fatal(err)
		}
		r, err := store.Reserve(ctx, item.clock, item.lease)
		if err != nil || r == nil || r.Task.ID != item.id {
			t.Fatalf("reserve: %+v %v", r, err)
		}
		if item.fail {
			if err := store.Fail(ctx, r, "test", now); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, item := range []struct {
		id string
		at time.Time
	}{{"ready", now.Add(-10 * time.Second)}, {"scheduled", now.Add(time.Second)}} {
		if err := store.Enqueue(ctx, &queue.Task{ID: item.id, MessageVersion: "job", AvailableAt: item.at}); err != nil {
			t.Fatal(err)
		}
	}
	// 模拟 Reserve 已迁移但尚未领取的 ready 成员，验证列表与过期租约合并统计。
	if err := store.client.ZRem(ctx, store.keys[2], "ready").Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.client.RPush(ctx, store.keys[1], "ready").Err(); err != nil {
		t.Fatal(err)
	}
	got, err := store.Stats(ctx, now)
	if err != nil || got.Ready != 2 || got.Scheduled != 1 || got.Running != 1 || got.Failed != 1 || !got.OldestReadyAt.Equal(now.Add(-2*time.Minute)) {
		t.Fatalf("stats: %+v %v", got, err)
	}
	// 大量 ready 列表成员无载荷：超过上限必须在读取载荷前返回，防止无界扫描。
	ids := make([]any, 1001)
	for i := range ids {
		ids[i] = "bounded-fixture"
	}
	if err := store.client.RPush(ctx, store.keys[1], ids...).Err(); err != nil {
		t.Fatal(err)
	}
	got, err = store.Stats(ctx, now)
	if err != nil || got.Ready != 1003 || got.OldestReadyKnown {
		t.Fatalf("bounded: %+v %v", got, err)
	}
}

func TestStatsRequiresContextTimeout(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "unused"})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	store, err := NewStore(testManager{client: client}, Config{Connection: "test", KeyPrefix: "stats"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stats(context.Background(), time.Now()); err == nil {
		t.Fatal("unbounded context accepted")
	}
}
