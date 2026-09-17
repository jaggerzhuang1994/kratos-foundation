package job

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestExecutionGateReportsSkipAndBalancedWaitMetrics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		metrics := &admissionMetricsRecorder{}
		gate := newExecutionGate(testModuleLog(t), "metrics", cronConfig{policy: DelayIfRunning, pending: 1}, nil, metrics)
		release := make(chan struct{})
		run := gate.middleware(func(context.Context) error { <-release; return nil })
		done := make(chan error, 2)
		go func() { done <- run(context.Background()) }()
		go func() { done <- run(context.Background()) }()
		synctest.Wait()
		if got := metrics.pendingValue(); got != 1 {
			t.Fatalf("pending metric = %d, want 1", got)
		}
		if err := run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := metrics.skippedReason("pending_full"); got != 1 {
			t.Fatalf("pending_full skips = %d, want 1", got)
		}
		close(release)
		synctest.Wait()
		for range 2 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		if got := metrics.pendingValue(); got != 0 || metrics.waitResult("admitted") != 1 {
			t.Fatalf("final metrics pending=%d admitted=%d", got, metrics.waitResult("admitted"))
		}

		cancelGate := newExecutionGate(testModuleLog(t), "cancel", cronConfig{policy: DelayIfRunning, pending: 1}, nil, metrics)
		cancelBlock := make(chan struct{})
		cancelRun := cancelGate.middleware(func(context.Context) error { <-cancelBlock; return nil })
		go func() { done <- cancelRun(context.Background()) }()
		synctest.Wait()
		waitCtx, cancelWait := context.WithCancel(context.Background())
		waitDone := make(chan error, 1)
		go func() { waitDone <- cancelRun(waitCtx) }()
		synctest.Wait()
		cancelWait()
		synctest.Wait()
		if err := <-waitDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled waiter error = %v", err)
		}
		if got := metrics.pendingValue(); got != 0 || metrics.waitResult("canceled") != 1 {
			t.Fatalf("canceled metrics pending=%d canceled=%d", got, metrics.waitResult("canceled"))
		}
		close(cancelBlock)
		synctest.Wait()
		<-done

		gate.update(cronConfig{policy: SkipIfRunning})
		block := make(chan struct{})
		skipRun := gate.middleware(func(context.Context) error { <-block; return nil })
		go func() { done <- skipRun(context.Background()) }()
		synctest.Wait()
		if err := skipRun(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := metrics.skippedReason("already_running"); got != 1 {
			t.Fatalf("already_running skips = %d, want 1", got)
		}
		close(block)
		synctest.Wait()
		<-done
		gate.update(cronConfig{disabled: true, policy: SkipIfRunning})
		if err := skipRun(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := metrics.skippedReason("disabled"); got != 1 {
			t.Fatalf("disabled skips = %d, want 1", got)
		}
	})
}

type admissionMetricsRecorder struct {
	mu      sync.Mutex
	skipped map[string]int
	pending int
	waits   map[string]int
}

func (r *admissionMetricsRecorder) reportSkipped(_ context.Context, _ string, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.skipped == nil {
		r.skipped = make(map[string]int)
	}
	r.skipped[reason]++
}

func (r *admissionMetricsRecorder) reportPending(_ context.Context, _ string, delta int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending += int(delta)
}

func (r *admissionMetricsRecorder) reportWait(_ context.Context, _ string, result string, _ time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.waits == nil {
		r.waits = make(map[string]int)
	}
	r.waits[result]++
}

func (r *admissionMetricsRecorder) pendingValue() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending
}

func (r *admissionMetricsRecorder) skippedReason(reason string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.skipped[reason]
}

func (r *admissionMetricsRecorder) waitResult(result string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.waits[result]
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

func TestDelayOverflowHandlerThroughManager(t *testing.T) {
	failure := errors.New("notification failed")
	for _, policy := range []ConcurrentPolicy{DelayIfRunning} {
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
					manager, err := NewManager(logger, spec, tracingProvider, metricsProvider, nil)
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

func TestPolicyReloadPreservesRunningAndPendingCalls(t *testing.T) {
	log := testModuleLog(t)
	synctest.Test(t, func(t *testing.T) {
		gate := newExecutionGate(log, "reload", cronConfig{policy: AllowOverlap, pending: 2}, nil)
		release := make(chan struct{})
		var runs atomic.Int32
		run := gate.middleware(func(context.Context) error { runs.Add(1); <-release; return nil })
		done := make(chan error, 4)
		for range 2 {
			go func() { done <- run(context.Background()) }()
		}
		synctest.Wait()
		gate.update(cronConfig{policy: SkipIfRunning})
		if err := run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if runs.Load() != 2 {
			t.Fatal("switching to skip lost running count")
		}
		gate.update(cronConfig{policy: DelayIfRunning, pending: 2})
		for range 2 {
			go func() { done <- run(context.Background()) }()
		}
		synctest.Wait()
		gate.mu.Lock()
		pending := gate.pending
		gate.mu.Unlock()
		if pending != 2 {
			t.Fatalf("pending=%d", pending)
		}
		gate.update(cronConfig{policy: DelayIfRunning, pending: 0})
		if err := run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if runs.Load() != 2 {
			t.Fatal("shrinking queue admitted another call")
		}
		gate.update(cronConfig{disabled: true, policy: SkipIfRunning})
		if err := run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if runs.Load() != 2 {
			t.Fatal("disabled gate admitted new invocation")
		}
		close(release)
		synctest.Wait()
		for range 4 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		if runs.Load() != 4 {
			t.Fatal("reload discarded already queued calls")
		}
		gate.mu.Lock()
		defer gate.mu.Unlock()
		if gate.running != 0 || gate.pending != 0 {
			t.Fatalf("leaked state: running=%d pending=%d", gate.running, gate.pending)
		}
	})
}

func TestGateReleasesExecutionAfterPanic(t *testing.T) {
	gate := newExecutionGate(testModuleLog(t), "panic", cronConfig{policy: DelayIfRunning, pending: 0}, nil)
	calls := 0
	run := gate.middleware(func(context.Context) error {
		calls++
		if calls == 1 {
			panic("failure")
		}
		return nil
	})
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected panic")
			}
		}()
		_ = run(context.Background())
	}()
	if err := run(context.Background()); err != nil || calls != 2 {
		t.Fatalf("slot leaked: %d %v", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
