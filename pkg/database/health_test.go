package database

import (
	"context"
	"errors"
	"testing"
)

// 主库检查只访问主库：副本不可用时仍通过，未知连接与已关闭的 Manager 返回对应错误。
func TestPrimaryCheck(t *testing.T) {
	m := newReplicaManager(t)
	ctx := context.Background()
	if err := PrimaryCheck(m, "")(ctx); err != nil {
		t.Fatalf("default connection: %v", err)
	}
	if err := PrimaryCheck(m, "primary")(ctx); err != nil {
		t.Fatalf("named connection: %v", err)
	}
	replica, ok := m.connectionFactory.pool("primary/replica-1")
	if !ok {
		t.Fatal("replica pool not tracked")
	}
	if err := replica.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := PrimaryCheck(m, "")(ctx); err != nil {
		t.Fatalf("primary check touched the closed replica: %v", err)
	}
	if err := PrimaryCheck(m, "missing")(ctx); !errors.Is(err, ErrConnectionUnknown) {
		t.Fatalf("unknown connection error = %v", err)
	}
	if err := m.close(); err != nil {
		t.Fatal(err)
	}
	if err := PrimaryCheck(m, "")(ctx); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("closed manager error = %v", err)
	}
}
