package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	goredis "github.com/redis/go-redis/v9"
)

type testManager struct {
	client *goredis.Client
	err    error
}

func (m testManager) Default() *goredis.Client                   { return m.client }
func (m testManager) Connection(string) (*goredis.Client, error) { return m.client, m.err }

type commandHook struct{ run func(goredis.Cmder) error }

func (h commandHook) DialHook(next goredis.DialHook) goredis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (h commandHook) ProcessHook(goredis.ProcessHook) goredis.ProcessHook {
	return func(_ context.Context, c goredis.Cmder) error { return h.run(c) }
}
func (h commandHook) ProcessPipelineHook(next goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return next
}

func TestStoreKeyPrefix(t *testing.T) {
	for _, prefix := range []string{"app:queue:email", "app:queue:report", "app:{email}", "custom:", " custom ", strings.Repeat("p", 65)} {
		t.Run(prefix, func(t *testing.T) {
			client := goredis.NewClient(&goredis.Options{Addr: "unused"})
			t.Cleanup(func() {
				if err := client.Close(); err != nil {
					t.Error(err)
				}
			})
			client.AddHook(commandHook{run: func(c goredis.Cmder) error {
				// 从实际发送的命令验证五个键，防止重新引入散列或隐式命名空间。
				want := []any{prefix + ":tasks", prefix + ":ready", prefix + ":delayed", prefix + ":reserved", prefix + ":failed"}
				if args := c.Args(); args[2] != 5 || !reflect.DeepEqual(args[3:8], want) {
					t.Fatalf("unexpected keys: %v", args)
				}
				c.(*goredis.Cmd).SetVal(int64(1))
				return nil
			}})
			s, err := NewStore(testManager{client: client}, Config{Connection: "redis", KeyPrefix: prefix})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Enqueue(context.Background(), &queue.Task{ID: "id", MessageVersion: "email"}); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, config := range []Config{
		{Connection: "redis"},
		{Connection: "redis", KeyPrefix: " \t"},
		{KeyPrefix: "app:queue:email"},
	} {
		if _, err := NewStore(testManager{}, config); err == nil {
			t.Fatalf("invalid config accepted: %+v", config)
		}
	}
}

func TestStore(t *testing.T) {
	ctx := context.Background()
	sentinel := errors.New("offline")
	if _, err := NewStore(testManager{}, Config{}); err == nil {
		t.Fatal("missing config accepted")
	}
	if _, err := NewStore(testManager{err: sentinel}, Config{Connection: "redis", KeyPrefix: "test"}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	client := goredis.NewClient(&goredis.Options{Addr: "unused"})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	var response any = int64(1)
	var commandErr error
	client.AddHook(commandHook{run: func(c goredis.Cmder) error {
		if commandErr != nil {
			return commandErr
		}
		c.(*goredis.Cmd).SetVal(response)
		return nil
	}})
	s, err := NewStore(testManager{client: client}, Config{Connection: "redis", KeyPrefix: "test"})
	if err != nil {
		t.Fatal(err)
	}
	task := &queue.Task{ID: "id", MessageVersion: "job", Payload: []byte{0, 255}}
	r := &queue.Reservation{Task: task, Token: "token"}
	if err := s.Enqueue(ctx, nil); err == nil {
		t.Fatal("nil accepted")
	}
	if err := s.Enqueue(ctx, task); err != nil {
		t.Fatal(err)
	}
	response = int64(0)
	if err := s.Enqueue(ctx, task); !errors.Is(err, queue.ErrDuplicate) {
		t.Fatal(err)
	}
	if _, err := s.Reserve(ctx, time.Now(), 0); err == nil {
		t.Fatal("zero lease accepted")
	}
	commandErr = goredis.Nil
	if value, err := s.Reserve(ctx, time.Now(), time.Second); err != nil || value != nil {
		t.Fatalf("%v %v", value, err)
	}
	commandErr = nil
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(map[string]any{"id": task.ID, "record": record{Task: string(raw), Token: "token", Attempts: 1}})
	if err != nil {
		t.Fatal(err)
	}
	response = string(envelope)
	got, err := s.Reserve(ctx, time.Now(), time.Second)
	if err != nil || got.Attempts != 1 || got.Task.Payload[1] != 255 {
		t.Fatalf("%v %v", got, err)
	}
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"ack", func() error { return s.Ack(ctx, r) }},
		{"release", func() error { return s.Release(ctx, r, time.Now()) }},
		{"fail", func() error { return s.Fail(ctx, r, "exhausted", time.Now()) }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			response = int64(1)
			if err := operation.run(); err != nil {
				t.Fatal(err)
			}
			response = int64(0)
			if err := operation.run(); !errors.Is(err, queue.ErrLeaseLost) {
				t.Fatal(err)
			}
			commandErr = sentinel
			if err := operation.run(); !errors.Is(err, sentinel) {
				t.Fatal(err)
			}
			commandErr = nil
		})
	}
	if err := s.Fail(ctx, r, strings.Repeat("x", 129), time.Now()); err == nil {
		t.Fatal("long reason accepted")
	}
	if err := s.Ack(ctx, nil); !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatal(err)
	}
	failed, err := json.Marshal(record{Task: string(raw), Attempts: 2, Reason: "exhausted", FailedAt: 123})
	if err != nil {
		t.Fatal(err)
	}
	response = []any{string(failed)}
	failures, err := s.Failed(ctx, 1)
	if err != nil || len(failures) != 1 || failures[0].Attempts != 2 {
		t.Fatalf("%v %v", failures, err)
	}
	if _, err := s.Failed(ctx, 0); err == nil {
		t.Fatal("invalid limit accepted")
	}
	response = int64(1)
	if err := s.Retry(ctx, "id", time.Now()); err != nil {
		t.Fatal(err)
	}
	response = int64(0)
	if err := s.Retry(ctx, "id", time.Now()); !errors.Is(err, queue.ErrNotFound) {
		t.Fatal(err)
	}
	commandErr = sentinel
	if err := s.Enqueue(ctx, task); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if _, err := s.Reserve(ctx, time.Now(), time.Second); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if _, err := s.Failed(ctx, 1); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if err := s.Retry(ctx, "id", time.Now()); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
}

func integrationStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	addr := os.Getenv("FOUNDATION_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set FOUNDATION_TEST_REDIS_ADDR for isolated Redis Lua integration tests")
	}
	client := goredis.NewClient(&goredis.Options{Addr: addr, MaxRetries: -1, ContextTimeoutEnabled: true})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	s, err := NewStore(testManager{client: client}, Config{Connection: "test", KeyPrefix: "test:queue:" + uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanCancel()
		if err := client.Del(cleanCtx, s.keys...).Err(); err != nil {
			t.Error(err)
		}
		if err := client.Close(); err != nil {
			t.Error(err)
		}
		cancel()
	})
	return s, ctx
}

func TestStoreRedisLifecycle(t *testing.T) {
	s, ctx := integrationStore(t)
	now := time.Unix(1700000000, 0)
	task := &queue.Task{ID: "id", MessageVersion: "job", Payload: []byte{0, 255}, Headers: map[string]string{"trace": "test"}, AvailableAt: now.Add(time.Second), CreatedAt: now}
	if err := s.Enqueue(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue(ctx, task); !errors.Is(err, queue.ErrDuplicate) {
		t.Fatal(err)
	}
	if r, err := s.Reserve(ctx, now, time.Second); err != nil || r != nil {
		t.Fatalf("%v %v", r, err)
	}
	now = now.Add(time.Second)
	first, err := s.Reserve(ctx, now, time.Second)
	if err != nil || first == nil {
		t.Fatalf("%v %v", first, err)
	}
	if first.Attempts != 1 || first.Task.Payload[1] != 255 {
		t.Fatalf("%+v", first)
	}
	second, err := s.Reserve(ctx, now.Add(time.Second), time.Second)
	if err != nil || second == nil {
		t.Fatalf("%v %v", second, err)
	}
	if second.Attempts != 2 || second.Token == first.Token {
		t.Fatal("lease not renewed")
	}
	for _, err := range []error{s.Ack(ctx, first), s.Release(ctx, first, now), s.Fail(ctx, first, "old", now)} {
		if !errors.Is(err, queue.ErrLeaseLost) {
			t.Fatal(err)
		}
	}
	if err := s.Release(ctx, second, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if r, err := s.Reserve(ctx, now.Add(4*time.Second), time.Second); err != nil || r != nil {
		t.Fatalf("%v %v", r, err)
	}
	third, err := s.Reserve(ctx, now.Add(5*time.Second), time.Second)
	if err != nil || third == nil {
		t.Fatalf("%v %v", third, err)
	}
	if !third.Task.AvailableAt.Equal(now.Add(5 * time.Second)) {
		t.Fatal(third.Task.AvailableAt)
	}
	if third.Attempts != 3 {
		t.Fatal(third.Attempts)
	}
	if err := s.Fail(ctx, third, "exhausted", now); err != nil {
		t.Fatal(err)
	}
	failed, err := s.Failed(ctx, 10)
	if err != nil || len(failed) != 1 || failed[0].Reason != "exhausted" || failed[0].Attempts != 3 {
		t.Fatalf("%v %v", failed, err)
	}
	if err := s.Retry(ctx, "id", now); err != nil {
		t.Fatal(err)
	}
	retried, err := s.Reserve(ctx, now, time.Second)
	if err != nil || retried == nil || retried.Attempts != 1 {
		t.Fatalf("%v %v", retried, err)
	}
	if !retried.Task.AvailableAt.Equal(now) {
		t.Fatal(retried.Task.AvailableAt)
	}
	if err := s.Ack(ctx, retried); err != nil {
		t.Fatal(err)
	}
	if err := s.Retry(ctx, "id", now); !errors.Is(err, queue.ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.Enqueue(ctx, task); err != nil {
		t.Fatal(err)
	}
}

func TestStoreRedisConcurrentReserve(t *testing.T) {
	s, ctx := integrationStore(t)
	now := time.Unix(1700000000, 0)
	if err := s.Enqueue(ctx, &queue.Task{ID: "one", MessageVersion: "job", AvailableAt: now}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan *queue.Reservation, 16)
	for range 16 {
		wg.Go(func() {
			r, err := s.Reserve(ctx, now, time.Minute)
			if err != nil {
				t.Error(err)
				return
			}
			if r != nil {
				results <- r
			}
		})
	}
	wg.Wait()
	close(results)
	if len(results) != 1 {
		t.Fatalf("got %d owners", len(results))
	}
}

func TestStoreRedisCorruptPayload(t *testing.T) {
	s, ctx := integrationStore(t)
	now := time.Unix(1700000000, 0)
	for _, raw := range []string{`{"task":"broken","attempts":0,"token":""}`, `broken envelope`} {
		if err := s.client.HSet(ctx, s.keys[0], "broken", raw).Err(); err != nil {
			t.Fatal(err)
		}
		if err := s.client.ZAdd(ctx, s.keys[2], goredis.Z{Score: float64(now.UnixMilli()), Member: "broken"}).Err(); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Reserve(ctx, now, time.Second); err == nil {
			t.Fatal("corrupt record accepted")
		}
		if r, err := s.Reserve(ctx, now.Add(time.Hour), time.Second); err != nil || r != nil {
			t.Fatalf("corrupt record loops: %v %v", r, err)
		}
		if _, err := s.Failed(ctx, 10); err == nil {
			t.Fatal("corruption hidden")
		}
	}
	t.Run("missing failed payload", func(t *testing.T) {
		s, ctx := integrationStore(t)
		if err := s.client.ZAdd(ctx, s.keys[4], goredis.Z{Score: 1, Member: "missing"}).Err(); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Failed(ctx, 10); err == nil {
			t.Fatal("missing failed record silently skipped")
		}
	})
}

func TestScheduledMillis(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   time.Time
		want int64
	}{
		{"aligned", time.Unix(0, 1000000), 1},
		{"fraction", time.Unix(0, 1000001), 2},
		{"negative", time.Unix(-1, 999999999), 0},
		{"zero time", time.Time{}, time.Time{}.UnixMilli()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := scheduledMillis(tc.at); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}

func TestStoreRedisPrecisionAndPayload(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
		payload []byte
	}{
		{"nil", nil, nil},
		{"empty", map[string]string{}, []byte{}},
		{"binary", map[string]string{"追踪": "测试🚀"}, []byte{0, 255, 128, 34, 92}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx := integrationStore(t)
			base := time.Unix(1700000000, 0)
			at := base.Add(100 * time.Microsecond)
			original := &queue.Task{ID: "精度", MessageVersion: "任务🚀", Headers: tc.headers, Payload: tc.payload, AvailableAt: at, CreatedAt: base}
			if err := s.Enqueue(ctx, original); err != nil {
				t.Fatal(err)
			}
			assertEmpty := func(now time.Time) {
				t.Helper()
				r, err := s.Reserve(ctx, now, time.Millisecond)
				if err != nil || r != nil {
					t.Fatalf("early reservation: %v %v", r, err)
				}
			}
			assertTask := func(now time.Time) *queue.Reservation {
				t.Helper()
				r, err := s.Reserve(ctx, now, time.Millisecond)
				if err != nil || r == nil {
					t.Fatalf("missing reservation: %v %v", r, err)
				}
				if r.Task.MessageVersion != original.MessageVersion || !reflect.DeepEqual(r.Task.Payload, original.Payload) || !reflect.DeepEqual(r.Task.Headers, original.Headers) {
					t.Fatalf("payload changed: %#v", r.Task)
				}
				return r
			}
			assertEmpty(base)
			assertEmpty(at.Add(-time.Nanosecond))
			first := assertTask(base.Add(time.Millisecond + 100*time.Microsecond))
			// 租约实际截止于2.1ms；2.0ms不能因截断而提前回收。
			assertEmpty(base.Add(2 * time.Millisecond))
			second := assertTask(base.Add(3 * time.Millisecond))
			if second.Token == first.Token || second.Attempts != 2 {
				t.Fatal("lease was not reclaimed")
			}
			releaseAt := base.Add(5*time.Millisecond + 100*time.Microsecond)
			if err := s.Release(ctx, second, releaseAt); err != nil {
				t.Fatal(err)
			}
			assertEmpty(base.Add(5 * time.Millisecond))
			third := assertTask(base.Add(6 * time.Millisecond))
			if !third.Task.AvailableAt.Equal(releaseAt) {
				t.Fatal(third.Task.AvailableAt)
			}
			if err := s.Fail(ctx, third, "manual", base); err != nil {
				t.Fatal(err)
			}
			retryAt := base.Add(8*time.Millisecond + 100*time.Microsecond)
			if err := s.Retry(ctx, original.ID, retryAt); err != nil {
				t.Fatal(err)
			}
			assertEmpty(base.Add(8 * time.Millisecond))
			fourth := assertTask(base.Add(9 * time.Millisecond))
			if !fourth.Task.AvailableAt.Equal(retryAt) || fourth.Attempts != 1 {
				t.Fatal(fourth)
			}
		})
	}
}

func TestStoreRedisKeyTypeFailurePreservesState(t *testing.T) {
	for _, operation := range []string{"enqueue", "reserve", "ack", "release", "fail", "failed", "retry"} {
		for index := range 5 {
			t.Run(fmt.Sprintf("%s/key%d", operation, index), func(t *testing.T) {
				s, ctx := integrationStore(t)
				now := time.Unix(1700000000, 0)
				if err := s.Enqueue(ctx, &queue.Task{ID: "existing", MessageVersion: "test", AvailableAt: now}); err != nil {
					t.Fatal(err)
				}
				var r *queue.Reservation
				if operation != "enqueue" && operation != "reserve" {
					var err error
					r, err = s.Reserve(ctx, now, time.Second)
					if err != nil || r == nil {
						t.Fatalf("prepare reservation: %v %v", r, err)
					}
					if operation == "failed" || operation == "retry" {
						if err := s.Fail(ctx, r, "test", now); err != nil {
							t.Fatal(err)
						}
					}
				}
				run := func() error {
					switch operation {
					case "enqueue":
						return s.Enqueue(ctx, &queue.Task{ID: "new", MessageVersion: "test", AvailableAt: now})
					case "reserve":
						value, err := s.Reserve(ctx, now, time.Second)
						if err == nil && (value == nil || value.Task.ID != "existing" || value.Attempts != 1) {
							t.Fatalf("task lost or attempts changed: %v", value)
						}
						return err
					case "ack":
						return s.Ack(ctx, r)
					case "release":
						return s.Release(ctx, r, now)
					case "fail":
						return s.Fail(ctx, r, "test", now)
					case "failed":
						_, err := s.Failed(ctx, 10)
						return err
					default:
						return s.Retry(ctx, "existing", now)
					}
				}
				dump := func(key string) string {
					value, err := s.client.Dump(ctx, key).Result()
					if err != nil && !errors.Is(err, goredis.Nil) {
						t.Fatal(err)
					}
					return value
				}
				original := dump(s.keys[index])
				if err := s.client.Set(ctx, s.keys[index], "wrong-type", 0).Err(); err != nil {
					t.Fatal(err)
				}
				before := make([]string, len(s.keys))
				for i, key := range s.keys {
					before[i] = dump(key)
				}
				if err := run(); err == nil {
					t.Fatal("key type corruption accepted")
				}
				for i, key := range s.keys {
					if dump(key) != before[i] {
						t.Fatalf("failed operation changed key %d", i)
					}
				}
				// 恢复受损键后，原操作必须可重试；不能留下孤立正文或丢失领取次数。
				if err := s.client.Del(ctx, s.keys[index]).Err(); err != nil {
					t.Fatal(err)
				}
				if original != "" {
					if err := s.client.Restore(ctx, s.keys[index], 0, original).Err(); err != nil {
						t.Fatal(err)
					}
				}
				if err := run(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
