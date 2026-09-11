package redis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"testing"

	lockredis "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/lock/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	goredis "github.com/redis/go-redis/v9"
)

type managerStub struct {
	client *goredis.Client
	name   string
}

func (m *managerStub) Default() *goredis.Client {
	return m.client
}

func (m *managerStub) Connection(name string) (*goredis.Client, error) {
	m.name = name
	return m.client, nil
}

func TestNewLockCoordinatorUsesNamedConnection(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:0"})
	t.Cleanup(func() { _ = client.Close() })
	manager := &managerStub{client: client}

	coordinator, err := NewLockCoordinator(
		manager,
		job.LockCoordinatorConfig{},
		lockredis.WithConnection("locks"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if coordinator == nil {
		t.Fatal("coordinator is nil")
	}
	if manager.name != "locks" {
		t.Fatalf("connection name = %q", manager.name)
	}
}

func TestNewDefaultLockCoordinatorUsesDefaultConnection(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:0"})
	t.Cleanup(func() { _ = client.Close() })
	coordinator, err := NewDefaultLockCoordinator(&managerStub{client: client})
	if err != nil || coordinator == nil {
		t.Fatalf("NewDefaultLockCoordinator() = %v, %v", coordinator, err)
	}
}

func TestExternalRedisCoordinationRenewsAndReleases(t *testing.T) {
	address := os.Getenv("FOUNDATION_TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("set FOUNDATION_TEST_REDIS_ADDR for Docker integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clients := []*goredis.Client{goredis.NewClient(&goredis.Options{Addr: address}), goredis.NewClient(&goredis.Options{Addr: address})}
	for _, client := range clients {
		t.Cleanup(func() {
			if err := client.Close(); err != nil {
				t.Error(err)
			}
		})
		if err := client.Ping(ctx).Err(); err != nil {
			t.Fatal(err)
		}
	}
	config := job.LockCoordinatorConfig{KeyPrefix: fmt.Sprintf("foundation-test-%d:", time.Now().UnixNano()), LeaseTTL: 600 * time.Millisecond, RefreshInterval: 100 * time.Millisecond, OperationTimeout: 150 * time.Millisecond}
	first, err := NewLockCoordinator(&managerStub{client: clients[0]}, config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewLockCoordinator(&managerStub{client: clients[1]}, config)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := first.Acquire(ctx, "job")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = guard.Release() })
	// 跨过原始租约 TTL，实际 Redis 中的续租必须仍阻止第二客户端取得执行权。
	time.Sleep(900 * time.Millisecond)
	if _, err := second.TryAcquire(ctx, "job"); !errors.Is(err, job.ErrExecutionInProgress) {
		t.Fatalf("renewed lock=%v", err)
	}
	if err := guard.Release(); err != nil {
		t.Fatal(err)
	}
	next, err := second.TryAcquire(ctx, "job")
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Release(); err != nil {
		t.Fatal(err)
	}
	t.Log("cross-client exclusion, renewal beyond TTL, and release passed")
}
