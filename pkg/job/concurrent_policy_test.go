package job

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

// This kills mutations that reverse declared middleware order or invoke nil middleware.
func TestChainMiddlewaresPreservesDeclaredNesting(t *testing.T) {
	var events []string
	wrap := func(name string) Middleware {
		return func(next Handler) Handler {
			return func(ctx context.Context) error {
				events = append(events, name+":before")
				err := next(ctx)
				events = append(events, name+":after")
				return err
			}
		}
	}
	err := chainMiddlewares(wrap("one"), nil, wrap("two"))(func(context.Context) error { events = append(events, "run"); return nil })(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"one:before", "two:before", "run", "two:after", "one:after"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v", events)
	}
}

func TestDelayAndSkipPolicies(t *testing.T) {
	log := testModuleLog(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	finished := make(chan struct{})
	delay := delayIfStillRunning(log)(func(context.Context) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return nil
	})
	go func() { _ = delay(context.Background()); finished <- struct{}{} }()
	waitFor(t, entered)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := delay(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("delayed canceled call = %v", err)
	}
	close(release)
	waitFor(t, finished)

	runs := 0
	hold := make(chan struct{})
	started := make(chan struct{})
	skip := skipIfStillRunning(log)(func(context.Context) error { runs++; close(started); <-hold; return nil })
	skipDone := make(chan error, 1)
	go func() { skipDone <- skip(context.Background()) }()
	waitFor(t, started)
	if err := skip(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("skip runs = %d", runs)
	}
	close(hold)
	if err := waitForValue(t, skipDone); err != nil {
		t.Fatalf("first skip run = %v", err)
	}
}

func TestDistributedConcurrentJoinsRunCoordinationAndReleaseErrors(t *testing.T) {
	lostCtx, cancel := context.WithCancelCause(context.Background())
	cancel(ErrCoordinationLost)
	guard := &testGuard{ctx: lostCtx, releaseErr: errors.New("release")}
	coordinator := &testCoordinator{guard: guard}
	runErr := errors.New("run")
	err := distributedConcurrent(testModuleLog(t), coordinator, "billing", true)(func(context.Context) error { return runErr })(context.Background())
	if !errors.Is(err, runErr) || !errors.Is(err, ErrCoordinationLost) || !strings.Contains(err.Error(), "release") {
		t.Fatalf("joined error = %v", err)
	}
	if coordinator.acquireCalls != 1 || guard.releases != 1 || coordinator.key != "billing" {
		t.Fatalf("coordination = %#v guard=%#v", coordinator, guard)
	}

	coordinator = &testCoordinator{tryErr: ErrExecutionInProgress}
	err = distributedConcurrent(testModuleLog(t), coordinator, "billing", false)(func(context.Context) error { t.Fatal("skipped job ran"); return nil })(context.Background())
	if err != nil || coordinator.tryCalls != 1 {
		t.Fatalf("skip result = %v, calls=%d", err, coordinator.tryCalls)
	}
}

func TestConcurrentMiddlewareSelectsDeclaredPolicy(t *testing.T) {
	log := testModuleLog(t)
	if middleware := concurrentMiddleware(log, AllowOverlap, nil, "job"); middleware != nil {
		t.Fatal("AllowOverlap unexpectedly installed middleware")
	}
	if middleware := concurrentMiddleware(log, ConcurrentPolicy(99), nil, "job"); middleware != nil {
		t.Fatal("invalid policy unexpectedly installed middleware")
	}

	for _, policy := range []ConcurrentPolicy{DelayIfRunning, SkipIfRunning} {
		runs := 0
		middleware := concurrentMiddleware(log, policy, nil, "job")
		if middleware == nil {
			t.Fatalf("policy %d returned nil middleware", policy)
		}
		if err := middleware(func(context.Context) error { runs++; return nil })(context.Background()); err != nil {
			t.Fatalf("policy %d error = %v", policy, err)
		}
		if runs != 1 {
			t.Fatalf("policy %d runs = %d, want 1", policy, runs)
		}
	}

	for _, test := range []struct {
		policy      ConcurrentPolicy
		wantAcquire int
		wantTry     int
	}{
		{policy: DelayIfDistributedRunning, wantAcquire: 1},
		{policy: SkipIfDistributedRunning, wantTry: 1},
	} {
		guard := &testGuard{ctx: context.Background()}
		coordinator := &testCoordinator{guard: guard}
		middleware := concurrentMiddleware(log, test.policy, coordinator, "billing")
		if err := middleware(func(context.Context) error { return nil })(context.Background()); err != nil {
			t.Fatalf("policy %d error = %v", test.policy, err)
		}
		if coordinator.acquireCalls != test.wantAcquire || coordinator.tryCalls != test.wantTry || guard.releases != 1 {
			t.Fatalf("policy %d coordination = %#v, guard=%#v", test.policy, coordinator, guard)
		}
	}
}

func TestDistributedConcurrentRejectsCoordinatorFailuresAndNilGuard(t *testing.T) {
	wantErr := errors.New("coordinator unavailable")
	for _, test := range []struct {
		name        string
		coordinator *testCoordinator
		wait        bool
	}{
		{name: "acquire failure", coordinator: &testCoordinator{acquireErr: wantErr}, wait: true},
		{name: "try failure", coordinator: &testCoordinator{tryErr: wantErr}},
		{name: "nil guard", coordinator: &testCoordinator{}, wait: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ran := false
			err := distributedConcurrent(testModuleLog(t), test.coordinator, "billing", test.wait)(func(context.Context) error {
				ran = true
				return nil
			})(context.Background())
			if err == nil || !strings.Contains(err.Error(), "billing") {
				t.Fatalf("coordination error = %v", err)
			}
			if test.coordinator.acquireErr != nil || test.coordinator.tryErr != nil {
				if !errors.Is(err, wantErr) {
					t.Fatalf("coordination error = %v, want wrapped source", err)
				}
			}
			if ran {
				t.Fatal("task ran without coordination")
			}
		})
	}
}

func TestDistributedConcurrentReleasesGuardWhenTaskPanics(t *testing.T) {
	log, logPath := testFileModuleLog(t)
	guard := &testGuard{ctx: context.Background(), releaseErr: errors.New("release failed")}
	coordinator := &testCoordinator{guard: guard}
	func() {
		defer func() {
			if recovered := recover(); recovered != "boom" {
				t.Fatalf("recovered panic = %#v, want boom", recovered)
			}
		}()
		_ = distributedConcurrent(log, coordinator, "billing", true)(func(context.Context) error {
			panic("boom")
		})(context.Background())
	}()
	if guard.releases != 1 {
		t.Fatalf("guard releases after panic = %d, want 1", guard.releases)
	}
	written, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if logs := string(written); !strings.Contains(logs, "release job coordination after panic failed") || !strings.Contains(logs, "release failed") {
		t.Fatalf("panic cleanup log = %s", logs)
	}
}

func TestDistributedConcurrentIgnoresOrdinaryGuardCancellationCause(t *testing.T) {
	guardContext, cancel := context.WithCancel(context.Background())
	cancel()
	guard := &testGuard{ctx: guardContext}
	err := distributedConcurrent(
		testModuleLog(t),
		&testCoordinator{guard: guard},
		"billing",
		true,
	)(func(context.Context) error { return nil })(context.Background())
	if err != nil {
		t.Fatalf("ordinary guard cancellation error = %v", err)
	}
}
