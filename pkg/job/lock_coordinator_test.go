package job

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	foundationlock "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/lock"
)

type recordingLocker struct {
	lease   foundationlock.Lease
	lockErr error
	tryErr  error
	key     string
	ttl     time.Duration
	usedTry bool
}

func (l *recordingLocker) Lock(_ context.Context, key string, ttl time.Duration) (foundationlock.Lease, error) {
	l.key, l.ttl = key, ttl
	return l.lease, l.lockErr
}

func (l *recordingLocker) TryLock(_ context.Context, key string, ttl time.Duration) (foundationlock.Lease, error) {
	l.key, l.ttl, l.usedTry = key, ttl, true
	return l.lease, l.tryErr
}

type recordingLease struct {
	mu          sync.Mutex
	refreshErr  error
	unlockErr   error
	unlockCalls int
}

func (*recordingLease) Key() string                                    { return "" }
func (*recordingLease) TTL(context.Context) (time.Duration, error)     { return 0, nil }
func (l *recordingLease) Refresh(context.Context, time.Duration) error { return l.refreshErr }
func (l *recordingLease) Unlock(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.unlockCalls++
	return l.unlockErr
}

func TestNewLockCoordinatorValidatesConfiguration(t *testing.T) {
	locker := &recordingLocker{}
	for _, config := range []LockCoordinatorConfig{
		{LeaseTTL: -time.Second},
		{LeaseTTL: time.Second, RefreshInterval: time.Second},
		{LeaseTTL: time.Second, RefreshInterval: time.Millisecond, OperationTimeout: time.Second},
	} {
		if _, err := NewLockCoordinator(locker, config); err == nil {
			t.Fatalf("NewLockCoordinator(%+v) succeeded", config)
		}
	}
}

func TestAcquireNormalizesKeyUsesDefaultsAndReleasesOnce(t *testing.T) {
	lease := &recordingLease{}
	locker := &recordingLocker{lease: lease}
	coordinator, err := NewLockCoordinator(locker, LockCoordinatorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	guard, err := coordinator.Acquire(context.Background(), " task ")
	if err != nil {
		t.Fatal(err)
	}
	if locker.key != "job:task" || locker.ttl != 30*time.Second {
		t.Fatalf("Lock() = key %q ttl %s, want job:task and 30s", locker.key, locker.ttl)
	}
	if err := guard.Release(); err != nil {
		t.Fatal(err)
	}
	if err := guard.Release(); err != nil {
		t.Fatal(err)
	}
	if lease.unlockCalls != 1 {
		t.Fatalf("Unlock calls = %d, want 1", lease.unlockCalls)
	}
	if guard.Context().Err() == nil {
		t.Fatal("guard context remains active after release")
	}
}

func TestTryAcquireMapsContentionAndRejectsBadInput(t *testing.T) {
	locker := &recordingLocker{tryErr: foundationlock.ErrNotAcquired}
	coordinator, err := NewLockCoordinator(locker, LockCoordinatorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.TryAcquire(context.Background(), "task"); !errors.Is(err, ErrExecutionInProgress) {
		t.Fatalf("TryAcquire contention error = %v", err)
	}
	if !locker.usedTry {
		t.Fatal("TryAcquire did not use Locker.TryLock")
	}
	if _, err := coordinator.Acquire(context.Background(), " \t "); err == nil {
		t.Fatal("Acquire accepted empty key")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := coordinator.Acquire(cancelled, "task"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire canceled context error = %v", err)
	}
}

func TestAcquireReturnsCoordinationLostWhenUnlockLostOwnership(t *testing.T) {
	lease := &recordingLease{unlockErr: foundationlock.ErrNotHeld}
	coordinator, err := NewLockCoordinator(&recordingLocker{lease: lease}, LockCoordinatorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	guard, err := coordinator.Acquire(context.Background(), "task")
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Release(); !errors.Is(err, ErrCoordinationLost) {
		t.Fatalf("Release() error = %v, want coordination lost", err)
	}
}
