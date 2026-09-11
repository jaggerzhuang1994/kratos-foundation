package job

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
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
	if middleware := concurrentMiddleware(log, AllowOverlap, nil, "job", 1, nil); middleware != nil {
		t.Fatal("AllowOverlap unexpectedly installed middleware")
	}
	if middleware := concurrentMiddleware(log, ConcurrentPolicy(99), nil, "job", 1, nil); middleware != nil {
		t.Fatal("invalid policy unexpectedly installed middleware")
	}

	for _, policy := range []ConcurrentPolicy{DelayIfRunning, SkipIfRunning} {
		runs := 0
		middleware := concurrentMiddleware(log, policy, nil, "job", 1, nil)
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
		middleware := concurrentMiddleware(log, test.policy, coordinator, "billing", 1, nil)
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

func TestDelayPendingCapacityThroughManager(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limit  int
		custom bool
	}{
		{"default", 1, false}, {"no waiting", 0, true}, {"three waiting", 3, true}, {"explicit unlimited", -1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var runs atomic.Int32
			options := []CronOption{WithConcurrentPolicy(DelayIfRunning)}
			if tc.custom {
				options = append(options, WithMaxPendingRuns(tc.limit))
			}
			spec := NewSpec()
			spec.Option(WithLogging(false), WithMetrics(false), WithTracing(false))
			spec.RegisterCron("bounded", "@hourly", TaskFunc(func(ctx context.Context) error { runs.Add(1); <-ctx.Done(); return ctx.Err() }), options...)
			logger, tracingProvider, metricsProvider := newTestObservability(t)
			synctest.Test(t, func(t *testing.T) {
				manager, err := NewManager(logger, spec, tracingProvider, metricsProvider, nil)
				if err != nil {
					t.Fatal(err)
				}
				run := manager.cronJobs[0].managedJob.job.Run
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				admitted := tc.limit + 1
				if tc.limit < 0 {
					admitted = 4
				}
				done := make(chan error, admitted)
				for range admitted {
					go func() { done <- run(ctx) }()
				}
				synctest.Wait()
				if runs.Load() != 1 {
					t.Fatalf("concurrent handlers=%d", runs.Load())
				}
				if tc.limit >= 0 {
					for range 20 {
						if err := run(ctx); err != nil {
							t.Fatal(err)
						}
					}
				}
				if runs.Load() != 1 {
					t.Fatal("full queue executed extra handler")
				}
				cancel()
				synctest.Wait()
				for range admitted {
					if err := <-done; !errors.Is(err, context.Canceled) {
						t.Fatalf("cancel error=%v", err)
					}
				}
				before := runs.Load()
				nextCtx, nextCancel := context.WithCancel(context.Background())
				go func() { done <- run(nextCtx) }()
				synctest.Wait()
				if runs.Load() != before+1 {
					t.Fatal("cancellation leaked admission slot")
				}
				nextCancel()
				synctest.Wait()
				<-done
			})
		})
	}
}

func TestPendingCapacityReleasedAfterPanic(t *testing.T) {
	calls := 0
	run := limitPendingRuns(testModuleLog(t), DelayOverflow{MaxPendingRuns: 0}, nil, func(next Handler) Handler { return next })(func(context.Context) error {
		calls++
		if calls == 1 {
			panic("failure")
		}
		return nil
	})
	func() {
		defer func() {
			if recover() == nil {
				t.Error("missing panic")
			}
		}()
		_ = run(context.Background())
	}()
	if err := run(context.Background()); err != nil || calls != 2 {
		t.Fatalf("slot leaked: calls=%d err=%v", calls, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// waitingCoordinator 模拟跨进程执行权一直被其他节点持有，Acquire 只在取消后结束。
type waitingCoordinator struct{ entered chan struct{} }

func (c *waitingCoordinator) Acquire(ctx context.Context, _ string) (ExecutionGuard, error) {
	c.entered <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}
func (c *waitingCoordinator) TryAcquire(ctx context.Context, key string) (ExecutionGuard, error) {
	return c.Acquire(ctx, key)
}

func TestDistributedDelayBoundsAcquisitionWaiters(t *testing.T) {
	log := testModuleLog(t)
	synctest.Test(t, func(t *testing.T) {
		coordinator := &waitingCoordinator{entered: make(chan struct{}, 2)}
		run := concurrentMiddleware(log, DelayIfDistributedRunning, coordinator, "distributed", 1, nil)(func(context.Context) error { t.Error("unexpected execution"); return nil })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 2)
		for range 2 {
			go func() { done <- run(ctx) }()
		}
		synctest.Wait()
		if len(coordinator.entered) != 2 {
			t.Fatal("expected two acquisition waiters")
		}
		for range 20 {
			if err := run(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if len(coordinator.entered) != 2 {
			t.Fatal("overflow reached coordinator")
		}
		cancel()
		synctest.Wait()
		for range 2 {
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			<-coordinator.entered
		}
		nextCtx, nextCancel := context.WithCancel(context.Background())
		go func() { done <- run(nextCtx) }()
		synctest.Wait()
		if len(coordinator.entered) != 1 {
			t.Fatal("canceled acquisition leaked slot")
		}
		nextCancel()
		synctest.Wait()
		<-done
	})
}

func TestDelayOverflowHandlerThroughManager(t *testing.T) {
	failure := errors.New("notification failed")
	for _, policy := range []ConcurrentPolicy{DelayIfRunning, DelayIfDistributedRunning} {
		for _, outcome := range []string{"success", "error", "panic"} {
			t.Run(fmt.Sprintf("%d/%s", policy, outcome), func(t *testing.T) {
				logger, tracingProvider, metricsProvider := newTestObservability(t)
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					var calls atomic.Int32
					spec := NewSpec()
					spec.Option(WithLogging(false), WithMetrics(false), WithTracing(false))
					spec.RegisterCron("notify", "@hourly", TaskFunc(func(ctx context.Context) error {
						<-ctx.Done()
						return ctx.Err()
					}), WithConcurrentPolicy(policy), WithMaxPendingRuns(0), WithDelayOverflowHandler(func(got context.Context, event DelayOverflow) error {
						calls.Add(1)
						if got != ctx || event != (DelayOverflow{Name: "notify", Policy: policy, MaxPendingRuns: 0}) {
							t.Errorf("unexpected event/context: %+v", event)
						}
						switch outcome {
						case "error":
							return failure
						case "panic":
							panic("notification panic")
						}
						return nil
					}))
					coordinator := &waitingCoordinator{entered: make(chan struct{}, 1)}
					manager, err := NewManager(logger, spec, tracingProvider, metricsProvider, coordinator)
					if err != nil {
						t.Fatal(err)
					}
					task := manager.cronJobs[0].managedJob.job
					done := make(chan error, 1)
					go func() { done <- task.Run(ctx) }()
					synctest.Wait()
					if calls.Load() != 0 {
						t.Fatal("notified without overflow")
					}
					var reported error
					trigger := cronJob{ctx: ctx, name: "notify", job: task, errorHandler: func(_ context.Context, name string, err error) {
						if name != "notify" {
							t.Error(name)
						}
						reported = err
					}}
					trigger.Run()
					if calls.Load() != 1 {
						t.Fatal("missing overflow notification")
					}
					switch outcome {
					case "success":
						if reported != nil {
							t.Fatal(reported)
						}
					case "error":
						if !errors.Is(reported, failure) {
							t.Fatal(reported)
						}
					case "panic":
						if reported == nil || !strings.Contains(reported.Error(), "notification panic") {
							t.Fatal(reported)
						}
					}
					cancel()
					synctest.Wait()
					if err := <-done; !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					if err := task.Run(ctx); !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					if calls.Load() != 1 {
						t.Fatal("cancellation notified overflow")
					}
				})
			})
		}
	}
}
