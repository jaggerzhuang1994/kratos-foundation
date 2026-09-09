package redis

import (
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
