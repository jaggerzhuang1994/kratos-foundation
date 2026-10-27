package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestWorkerLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs, _ := testObservability(t)
		store := &storeStub{reserve: func(context.Context, time.Time, time.Duration) (*Reservation, error) { return nil, nil }}
		w, err := newWorker[*Task](workerConfig{Name: "worker", Queue: "mail", Concurrency: 2}, store, map[string]taskHandler{"x": func(context.Context, *Task) error { return nil }}, obs)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- w.Start(context.Background()) }()
		synctest.Wait()
		if err = w.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err = <-done; err != nil {
			t.Fatal(err)
		}
		logs := strings.Join(obs.Logger.(*testLog).events, "\n")
		for _, event := range []string{"queue.worker.started", "queue.worker.stopped"} {
			if strings.Count(logs, "INFO event"+event) != 1 || !strings.Contains(logs, "queuemailworkerworker") {
				t.Fatalf("incomplete worker lifecycle log: %s", logs)
			}
		}
		if err = w.Start(context.Background()); err == nil {
			t.Fatal("restarted worker")
		}
		if err = w.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		w, err = newWorker[*Task](workerConfig{Name: "worker", Queue: "mail"}, store, map[string]taskHandler{"x": func(context.Context, *Task) error { return nil }}, obs)
		if err != nil {
			t.Fatal(err)
		}
		if err = w.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err = w.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

// 存储瞬时故障（含存储操作自身超时、与租约冲突合并的故障）不退出 Worker，按 500ms 起倍增退避重试，恢复后记录一次。
func TestWorkerRetriesTransientStorageErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs, _ := testObservability(t)
		failures := []error{errors.New("offline"), context.Canceled, context.DeadlineExceeded, errors.Join(ErrLeaseLost, errors.New("offline"))}
		var calls []time.Time
		recovered := make(chan struct{})
		store := &storeStub{reserve: func(context.Context, time.Time, time.Duration) (*Reservation, error) {
			calls = append(calls, time.Now())
			if len(calls) <= len(failures) {
				return nil, failures[len(calls)-1]
			}
			if len(calls) == len(failures)+1 {
				close(recovered)
			}
			return nil, nil
		}}
		w, err := newWorker[*Task](workerConfig{Name: "w", Queue: "q"}, store, map[string]taskHandler{"x": func(context.Context, *Task) error { return nil }}, obs)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- w.Start(context.Background()) }()
		<-recovered
		synctest.Wait()
		if err := w.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatalf("worker exited on transient storage error: %v", err)
		}
		for i, want := range []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second} {
			if got := calls[i+1].Sub(calls[i]); got != want {
				t.Fatalf("retry %d after %v, want %v", i+1, got, want)
			}
		}
		logs := strings.Join(obs.Logger.(*testLog).events, "\n")
		if strings.Count(logs, "WARN eventqueue.storage.retrying") != len(failures) || strings.Count(logs, "INFO eventqueue.storage.recovered") != 1 || strings.Contains(logs, "queue.storage.failed") {
			t.Fatalf("storage retry logs: %s", logs)
		}
		families, err := obs.Metrics.PrometheusGatherer().Gather()
		if err != nil {
			t.Fatal(err)
		}
		var runtimeFailures float64
		for _, family := range families {
			if family.GetName() == "queue_consumer_runtime_failures_total" {
				for _, metric := range family.GetMetric() {
					runtimeFailures += metric.GetCounter().GetValue()
				}
			}
		}
		if runtimeFailures != float64(len(failures)) {
			t.Fatalf("runtime failures = %v, want %d", runtimeFailures, len(failures))
		}
	})
}

// Store 返回不满足契约的领取结果属于实现缺陷，Worker 退出而不是重试。
func TestWorkerStopsOnInvalidReservation(t *testing.T) {
	obs, _ := testObservability(t)
	store := &storeStub{reserve: func(context.Context, time.Time, time.Duration) (*Reservation, error) { return &Reservation{}, nil }}
	w, err := newWorker[*Task](workerConfig{Name: "w", Queue: "q"}, store, map[string]taskHandler{"x": func(context.Context, *Task) error { return nil }}, obs)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Start(context.Background()); !errors.Is(err, errInvalidReservation) {
		t.Fatalf("invalid reservation error = %v", err)
	}
}

func TestWorkerLeaseConflictAndStoredOutcome(t *testing.T) {
	for _, joined := range []bool{false, true} {
		t.Run(fmt.Sprint(joined), func(t *testing.T) {
			obs, _ := testObservability(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			count := 0
			failure := errors.New("offline")
			store := &storeStub{reserve: func(context.Context, time.Time, time.Duration) (*Reservation, error) {
				count++
				if count > 1 {
					cancel()
					return nil, context.Canceled
				}
				return &Reservation{Task: &Task{MessageVersion: "x"}, Token: "t", Attempts: 1}, nil
			}, ack: func(context.Context, *Reservation) error {
				if joined {
					return errors.Join(ErrLeaseLost, failure)
				}
				return ErrLeaseLost
			}}
			w, err := newWorker[*Task](workerConfig{Name: "w", Queue: "q"}, store, map[string]taskHandler{"x": func(context.Context, *Task) error { return nil }}, obs)
			if err != nil {
				t.Fatal(err)
			}
			// 合并了存储故障的租约冲突按存储故障退避重试，纯租约冲突立即继续；两者都不退出。
			synctest.Test(t, func(t *testing.T) {
				if err := w.Start(ctx); err != nil || count != 2 {
					t.Fatalf("did not recover %d %v", count, err)
				}
			})
		})
	}
}

func TestWorkerStopDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs, _ := testObservability(t)
		entered := make(chan struct{})
		release := make(chan struct{})
		store := &storeStub{reserve: func(context.Context, time.Time, time.Duration) (*Reservation, error) {
			return &Reservation{Task: &Task{MessageVersion: "x"}, Token: "t", Attempts: 1}, nil
		}}
		w, err := newWorker[*Task](workerConfig{Name: "w", Queue: "q"}, store, map[string]taskHandler{"x": func(context.Context, *Task) error { close(entered); <-release; return nil }}, obs)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- w.Start(context.Background()) }()
		<-entered
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err = w.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("stop %v", err)
		}
		close(release)
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestWorkerConfigurationAndErrorClassification(t *testing.T) {
	obs, _ := testObservability(t)
	for _, tc := range []struct {
		name   string
		config workerConfig
	}{
		{"empty", workerConfig{}}, {"concurrency", workerConfig{Name: "w", Queue: "q", Concurrency: -1}}, {"window", workerConfig{Name: "w", Queue: "q", Lease: time.Second}}, {"retry", workerConfig{Name: "w", Queue: "q", Retry: &RetryPolicy{MaxAttempts: -1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newWorker[*Task](tc.config, nil, map[string]taskHandler{"x": func(context.Context, *Task) error { return nil }}, obs); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
	for _, handlers := range []map[string]taskHandler{nil, {"x": nil}, {" ": func(context.Context, *Task) error { return nil }}} {
		if _, err := newWorker[*Task](workerConfig{Name: "w", Queue: "q"}, nil, handlers, obs); err == nil {
			t.Fatal("accepted handlers")
		}
	}
}

// renewingStore 记录续租调用；lostAfter 次后返回 ErrLeaseLost。
type renewingStore struct {
	*storeStub
	canRenew  bool
	lostAfter int
	renewals  []time.Time
}

func (s *renewingStore) CanRenewLease() bool { return s.canRenew }
func (s *renewingStore) RenewLease(context.Context, *Reservation, time.Time) error {
	s.renewals = append(s.renewals, time.Now())
	if s.lostAfter > 0 && len(s.renewals) >= s.lostAfter {
		return ErrLeaseLost
	}
	return nil
}

// 开启 RenewLease 后，执行时长可超过租约：每 Lease/3 续租一次，任务正常确认。
func TestWorkerRenewsLeaseDuringLongHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs, _ := testObservability(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		reserved := false
		acked := make(chan struct{})
		store := &renewingStore{canRenew: true, storeStub: &storeStub{
			reserve: func(context.Context, time.Time, time.Duration) (*Reservation, error) {
				if reserved {
					return nil, nil
				}
				reserved = true
				return &Reservation{Task: &Task{ID: "long", MessageVersion: "x"}, Token: "t", Attempts: 1}, nil
			},
			ack: func(context.Context, *Reservation) error { close(acked); return nil },
		}}
		w, err := newWorker[*Task](workerConfig{Name: "w", Queue: "q", RenewLease: true, Lease: 3 * time.Second, StorageTimeout: 500 * time.Millisecond, Timeout: time.Minute}, store,
			map[string]taskHandler{"x": func(ctx context.Context, _ *Task) error { time.Sleep(10 * time.Second); return ctx.Err() }}, obs)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- w.Start(ctx) }()
		<-acked
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if len(store.renewals) != 10 {
			t.Fatalf("renewals = %d, want 10 (every 1s over 10s)", len(store.renewals))
		}
	})
}

// 续租发现租约已被他人取得时取消 Handler，不再确认任务。
func TestWorkerCancelsHandlerWhenRenewalLosesLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs, _ := testObservability(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		reserved := false
		canceled := make(chan time.Duration, 1)
		store := &renewingStore{canRenew: true, lostAfter: 2, storeStub: &storeStub{
			reserve: func(context.Context, time.Time, time.Duration) (*Reservation, error) {
				if reserved {
					return nil, nil
				}
				reserved = true
				return &Reservation{Task: &Task{ID: "long", MessageVersion: "x"}, Token: "t", Attempts: 1}, nil
			},
			release: func(context.Context, *Reservation, time.Time) error { return ErrLeaseLost },
		}}
		w, err := newWorker[*Task](workerConfig{Name: "w", Queue: "q", RenewLease: true, Lease: 3 * time.Second, StorageTimeout: 500 * time.Millisecond, Timeout: time.Minute}, store,
			map[string]taskHandler{"x": func(ctx context.Context, _ *Task) error {
				started := time.Now()
				<-ctx.Done()
				canceled <- time.Since(started)
				return ctx.Err()
			}}, obs)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- w.Start(ctx) }()
		if after := <-canceled; after != 2*time.Second {
			t.Fatalf("handler canceled after %v, want 2s", after)
		}
		synctest.Wait()
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if logs := strings.Join(obs.Logger.(*testLog).events, "\n"); strings.Count(logs, "WARN eventqueue.lease.lost") != 2 {
			t.Fatalf("lease lost logs: %s", logs)
		}
	})
}

func TestWorkerRenewLeaseValidation(t *testing.T) {
	obs, _ := testObservability(t)
	handlers := map[string]taskHandler{"x": func(context.Context, *Task) error { return nil }}
	for _, tc := range []struct {
		store  Store
		config workerConfig
	}{
		{&storeStub{}, workerConfig{Name: "w", Queue: "q", RenewLease: true}},
		{&renewingStore{storeStub: &storeStub{}}, workerConfig{Name: "w", Queue: "q", RenewLease: true}},
		{&renewingStore{canRenew: true, storeStub: &storeStub{}}, workerConfig{Name: "w", Queue: "q", RenewLease: true, Lease: 15 * time.Second}},
	} {
		if _, err := newWorker[*Task](tc.config, tc.store, handlers, obs); err == nil {
			t.Fatalf("accepted %+v", tc.config)
		}
	}
	// 续租时 Timeout 不受 Lease 限制，默认租约为 max(30s, 3×StorageTimeout+1s)。
	w, err := newWorker[*Task](workerConfig{Name: "w", Queue: "q", RenewLease: true, Timeout: time.Hour}, &renewingStore{canRenew: true, storeStub: &storeStub{}}, handlers, obs)
	if err != nil || w.config.Lease != 30*time.Second {
		t.Fatalf("lease=%v err=%v", w.config.Lease, err)
	}
}

// 时长约束违反时错误说明具体约束与实际值。
func TestWorkerConfigErrorsNameTheConstraint(t *testing.T) {
	for config, want := range map[workerConfig]string{
		{Name: "w", Queue: "q", Concurrency: 2000}:                                                          "concurrency 2000 must be between 1 and 1024",
		{Name: "w", Queue: "q", Timeout: time.Minute, Lease: 30 * time.Second}:                              "lease 30s must exceed timeout 1m0s + storage timeout 5s (or enable RenewLease)",
		{Name: "w", Queue: "q", RenewLease: true, Lease: 10 * time.Second, StorageTimeout: 5 * time.Second}: "lease 10s must exceed 3 x storage timeout 5s",
	} {
		if _, err := config.resolve(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: err = %v, want %q", config, err, want)
		}
	}
}
