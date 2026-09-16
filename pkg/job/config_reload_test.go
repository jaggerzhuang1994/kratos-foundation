package job

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
)

func TestConfigReloadIsAtomicAndDoesNotRepeatImmediateRun(t *testing.T) {
	spec := NewSpec()
	spec.RegisterCron("first", "@hourly", defaultsTask{})
	spec.RegisterCron("second", "@daily", defaultsTask{})
	scheduler := &testScheduler{}
	manager, err := newManager(testModuleLog(t), nil, spec, newManagerOptions(spec), scheduler, newScheduleParser(testModuleLog(t)), nil)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan error, 1)
	go func() { started <- manager.Start(context.Background()) }()
	waitFor(t, scheduler.startedCh())
	defer func() {
		if err := manager.Stop(context.Background()); err != nil {
			t.Error(err)
		}
		if err := <-started; err != nil {
			t.Error(err)
		}
	}()
	prior := manager.cronJobs[0].resolved
	invalid := &config_pb.Job{Cron: map[string]*config_pb.CronJob{
		"first":  {Schedule: proto.String("@every 1m"), ConcurrentPolicy: config_pb.JobConcurrentPolicy_SKIP_IF_RUNNING.Enum()},
		"second": {Schedule: proto.String("invalid")},
	}}
	if err := manager.applyConfig(invalid); err == nil {
		t.Fatal("invalid batch accepted")
	}
	if manager.cronJobs[0].resolved != prior {
		t.Fatal("partially applied invalid batch")
	}
	valid := &config_pb.Job{Cron: map[string]*config_pb.CronJob{"first": {Schedule: proto.String("@every 1m"), RunImmediately: proto.Bool(true), ConcurrentPolicy: config_pb.JobConcurrentPolicy_SKIP_IF_RUNNING.Enum(), MaxPendingRuns: proto.Int32(0)}}}
	if err := manager.applyConfig(valid); err != nil {
		t.Fatal(err)
	}
	calls, _, _ := scheduler.snapshot()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if next := calls[0].schedule.Next(now); !next.Equal(now.Add(time.Minute)) {
		t.Fatalf("hot update ran immediately: %v", next)
	}
	if err := manager.applyConfig(nil); err != nil {
		t.Fatal(err)
	}
	if manager.cronJobs[0].resolved != prior {
		t.Fatal("removing overrides did not restore registration/task defaults")
	}
	// Stop cancels subscriptions and prevents subsequent callbacks from publishing rules.
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.applyConfig(valid); err != nil {
		t.Fatal(err)
	}
	if manager.cronJobs[0].resolved != prior {
		t.Fatal("updated after stop")
	}
}

func TestConfigurationBeforeStartUsesLatestImmediateSetting(t *testing.T) {
	spec := NewSpec()
	spec.RegisterCron("task", "@hourly", TaskFunc(func(context.Context) error { return nil }))
	manager := newTestManager(t, spec)
	if err := manager.applyConfig(&config_pb.Job{Cron: map[string]*config_pb.CronJob{"task": {RunImmediately: proto.Bool(true)}}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if got := manager.cronJobs[0].scheduleSpec.Next(now); !got.Equal(now) {
		t.Fatal("pre-start immediate ignored")
	}
}

func TestRealConfigSubscriptionReschedulesAndStops(t *testing.T) {
	t.Setenv("CONFIG_POLL_INTERVAL", "100ms")
	logger, tracing, metrics := newTestObservability(t)
	synctest.Test(t, func(t *testing.T) {
		source := testconfig.NewMutableSource(t, "job", &config_pb.Job{Cron: map[string]*config_pb.CronJob{"refresh": {Schedule: proto.String("@every 1h"), RunImmediately: proto.Bool(false)}}})
		cfg, cleanup, err := config.NewManager(config.Sources{source})
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		var runs atomic.Int32
		spec := NewSpec()
		spec.RegisterCron("refresh", "@every 2h", TaskFunc(func(context.Context) error { runs.Add(1); return nil }), RunImmediately(true))
		manager, err := NewManager(logger, spec, tracing, metrics, cfg)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- manager.Start(context.Background()) }()
		synctest.Wait()
		if runs.Load() != 0 {
			t.Fatal("configured false ignored")
		}
		source.Update(t, &config_pb.Job{Cron: map[string]*config_pb.CronJob{"refresh": {Schedule: proto.String("@every 1s"), RunImmediately: proto.Bool(true)}}})
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		if runs.Load() != 0 {
			t.Fatal("hot update triggered immediate execution")
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if runs.Load() == 0 {
			t.Fatal("new cron schedule not applied")
		}
		source.Update(t, &config_pb.Job{Cron: map[string]*config_pb.CronJob{"refresh": {Schedule: proto.String("invalid")}}})
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		before := runs.Load()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if runs.Load() <= before {
			t.Fatal("invalid update discarded previous schedule")
		}
		if err := manager.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		before = runs.Load()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if runs.Load() != before {
			t.Fatal("job ran after stop")
		}
	})
}

// blockingSubscription 模拟 Start 建立订阅时 Stop 已经开始，不依赖真实时间或 I/O。
type blockingSubscription struct {
	config.Manager
	entered  chan struct{}
	proceed  chan struct{}
	canceled chan struct{}
}

func (c *blockingSubscription) Subscribe(string, any, config.Observer, ...any) (func(), error) {
	close(c.entered)
	<-c.proceed
	return func() { close(c.canceled) }, nil
}

func TestStopWaitsForInFlightSubscriptionSetup(t *testing.T) {
	logger, tracing, metrics := newTestObservability(t)
	cfg := &blockingSubscription{Manager: testconfig.Empty(t), entered: make(chan struct{}), proceed: make(chan struct{}), canceled: make(chan struct{})}
	spec := NewSpec()
	spec.RegisterCron("task", "@hourly", TaskFunc(func(context.Context) error { return nil }))
	manager, err := NewManager(logger, spec, tracing, metrics, cfg)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan error, 1)
	go func() { start <- manager.Start(context.Background()) }()
	waitFor(t, cfg.entered)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.Stop(ctx); err == nil {
		t.Fatal("Stop returned before subscription setup finished")
	}
	close(cfg.proceed)
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, cfg.canceled)
	if err := <-start; err != nil {
		t.Fatal(err)
	}
}

type failingJobConfig struct {
	config.Manager
	loadErr      error
	subscribeErr error
}

func (c *failingJobConfig) Load(key string, target any, defaults ...any) error {
	if c.loadErr != nil {
		return c.loadErr
	}
	return c.Manager.Load(key, target, defaults...)
}
func (c *failingJobConfig) Subscribe(key string, prototype any, observer config.Observer, defaults ...any) (func(), error) {
	if c.subscribeErr != nil {
		return nil, c.subscribeErr
	}
	return c.Manager.Subscribe(key, prototype, observer, defaults...)
}

func TestConfigStartupFailuresStopManager(t *testing.T) {
	logger, tracing, metrics := newTestObservability(t)
	for _, phase := range []string{"load", "subscribe"} {
		t.Run(phase, func(t *testing.T) {
			cfg := &failingJobConfig{Manager: testconfig.Empty(t)}
			spec := NewSpec()
			spec.RegisterCron("task", "@hourly", TaskFunc(func(context.Context) error { return nil }))
			manager, err := NewManager(logger, spec, tracing, metrics, cfg)
			if err != nil {
				t.Fatal(err)
			}
			failure := fmt.Errorf("%s failed", phase)
			if phase == "load" {
				cfg.loadErr = failure
			} else {
				cfg.subscribeErr = failure
			}
			if err := manager.Start(context.Background()); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if err := manager.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
