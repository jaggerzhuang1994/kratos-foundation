package job

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func TestNewManagerConstructsWithObservabilityDependencies(t *testing.T) {
	logger, tracingProvider, metricsProvider := newTestObservability(t)
	manager, err := NewManager(logger, NewSpec(), tracingProvider, metricsProvider, DefaultCoordinator())
	if err != nil {
		t.Fatal(err)
	}
	if manager == nil {
		t.Fatal("NewManager returned nil manager")
	}
}

func newTestManager(t *testing.T, spec *Spec) *Manager {
	t.Helper()
	logger, tracingProvider, metricsProvider := newTestObservability(t)
	manager, err := NewManager(logger, spec, tracingProvider, metricsProvider, nil)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func newTestObservability(t *testing.T) (log.Logger, tracing.Provider, metrics.Provider) {
	t.Helper()
	info := appinfo.New("test")
	shared, cleanupLog, err := testlog.New(testlog.Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: testlog.FileConfig{OutputConfig: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupLog)
	metricsProvider, cleanupMetrics, err := metrics.NewProvider(info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupMetrics)
	configManager := testconfig.Empty(t)
	tracingProvider, cleanupTracing, err := tracing.NewProvider(configManager, info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupTracing)
	return shared, tracingProvider, metricsProvider
}

type runtimeTracingProvider struct {
	provider trace.TracerProvider
	disabled bool
}

type runtimeMetricsProvider struct {
	metrics.Provider
	meter metric.Meter
	calls int
}

func (p *runtimeMetricsProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	p.calls++
	return p.meter
}

type failingRuntimeMeter struct {
	metric.Meter
	failInstrument string
	err            error
}

func (m failingRuntimeMeter) Int64Counter(
	string,
	...metric.Int64CounterOption,
) (metric.Int64Counter, error) {
	if m.failInstrument == "counter" {
		return nil, m.err
	}
	return m.Meter.Int64Counter(jobRunsInstrumentName)
}

func (m failingRuntimeMeter) Float64Histogram(
	string,
	...metric.Float64HistogramOption,
) (metric.Float64Histogram, error) {
	if m.failInstrument == "histogram" {
		return nil, m.err
	}
	return m.Meter.Float64Histogram(jobDurationInstrumentName)
}

func (m failingRuntimeMeter) Int64UpDownCounter(
	string,
	...metric.Int64UpDownCounterOption,
) (metric.Int64UpDownCounter, error) {
	if m.failInstrument == "up-down counter" {
		return nil, m.err
	}
	return m.Meter.Int64UpDownCounter(jobRunningInstrumentName)
}

func (p runtimeTracingProvider) Disabled() bool { return p.disabled }

func (p runtimeTracingProvider) TracerProvider() trace.TracerProvider { return p.provider }

func (p runtimeTracingProvider) Tracer(
	name string,
	options ...trace.TracerOption,
) trace.Tracer {
	return p.provider.Tracer(name, options...)
}

func TestNewManagerValidatesSpecBeforeCreatingMetrics(t *testing.T) {
	observability := newRuntimeObservability(t)
	metricsProvider := &runtimeMetricsProvider{Provider: observability.metricsProvider}
	invalid := NewSpec().RegisterOnce("missing-task", nil).(*Spec)
	manager, err := NewManager(
		observability.logger,
		invalid,
		observability.tracingProvider,
		metricsProvider,
		nil,
	)
	if manager != nil || err == nil || !strings.Contains(err.Error(), "missing-task") {
		t.Fatalf("NewManager(invalid spec) = (%v, %v)", manager, err)
	}
	if metricsProvider.calls != 0 {
		t.Fatalf("metrics provider called %d times before spec validation", metricsProvider.calls)
	}
}

func TestNewManagerPropagatesMetricsInitializationFailure(t *testing.T) {
	for _, instrument := range []string{"counter", "histogram", "up-down counter"} {
		t.Run(instrument, func(t *testing.T) {
			observability := newRuntimeObservability(t)
			wantErr := errors.New("meter unavailable")
			metricsProvider := &runtimeMetricsProvider{
				Provider: observability.metricsProvider,
				meter: failingRuntimeMeter{
					Meter:          observability.metricsProvider.Meter("failing"),
					failInstrument: instrument,
					err:            wantErr,
				},
			}
			manager, err := NewManager(
				observability.logger,
				NewSpec(),
				observability.tracingProvider,
				metricsProvider,
				nil,
			)
			if manager != nil || !errors.Is(err, wantErr) {
				t.Fatalf("NewManager(metrics failure) = (%v, %v)", manager, err)
			}
			if metricsProvider.calls != 1 {
				t.Fatalf("metrics provider calls = %d, want 1", metricsProvider.calls)
			}
		})
	}
}

func TestNewManagerRunsOneShotThroughConfiguredObservability(t *testing.T) {
	observability := newRuntimeObservability(t)
	var gotJobName string
	spec := NewSpec().
		RegisterOnce("invoice", TaskFunc(func(ctx context.Context) error {
			gotJobName = JobNameFromContext(ctx)
			return nil
		})).
		ExitWhenDone().(*Spec)
	manager, err := NewManager(
		observability.logger,
		spec,
		observability.tracingProvider,
		observability.metricsProvider,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != ErrCompleted {
		t.Fatalf("Start error = %v, want ErrStopRequested", err)
	}
	if gotJobName != "invoice" {
		t.Fatalf("task context job = %q", gotJobName)
	}
	if spans := observability.spans.GetSpans(); len(spans) != 1 || spans[0].Name != "invoice" || spans[0].Status.Code != codes.Ok {
		t.Fatalf("one-shot spans = %#v", spans)
	}
	if sample := findJobMetricSample(t, observability.metricsProvider, jobRunsInstrumentName, map[string]string{
		metricLabelJob: "invoice", metricLabelStatus: "success",
	}); sample.counter != 1 {
		t.Fatalf("one-shot counter = %v, want 1", sample.counter)
	}
	written, err := os.ReadFile(observability.logPath)
	if err != nil {
		t.Fatal(err)
	}
	if logs := string(written); !strings.Contains(logs, "module=job") || !strings.Contains(logs, "job=invoice") || !strings.Contains(logs, "job execution done") {
		t.Fatalf("one-shot logs = %s", logs)
	}
}

func TestNewManagerCanDisableObservabilityWithoutDisablingRecovery(t *testing.T) {
	observability := newRuntimeObservability(t)
	spec := NewSpec().
		Option(WithTracing(false), WithMetrics(false), WithLogging(false)).
		RegisterOnce("panic", TaskFunc(func(context.Context) error { panic("recovered") })).
		ExitWhenDone().(*Spec)
	manager, err := NewManager(
		observability.logger,
		spec,
		observability.tracingProvider,
		observability.metricsProvider,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	err = manager.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "job panic: recovered") {
		t.Fatalf("Start panic error = %v", err)
	}
	if spans := observability.spans.GetSpans(); len(spans) != 0 {
		t.Fatalf("disabled tracing exported %d spans", len(spans))
	}
	if _, err := os.Stat(observability.logPath); err == nil {
		written, readErr := os.ReadFile(observability.logPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(written) != 0 {
			t.Fatalf("disabled logging wrote %q", written)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestNewManagerInjectsConcurrencyCoordinator(t *testing.T) {
	for _, policy := range []ConcurrentPolicy{AllowOverlap, DelayIfRunning, SkipIfRunning, DelayIfDistributedRunning, SkipIfDistributedRunning} {
		t.Run(fmt.Sprint(policy), func(t *testing.T) {
			logger, tracingProvider, metricsProvider := newTestObservability(t)
			ran := false
			spec := NewSpec()
			spec.RegisterCron("injected", "@hourly", TaskFunc(func(context.Context) error { ran = true; return nil }), WithConcurrentPolicy(policy))
			manager, err := NewManager(logger, spec, tracingProvider, metricsProvider, nil)
			if policy.distributed() {
				if err == nil || manager != nil || !strings.Contains(err.Error(), "requires a concurrency coordinator") {
					t.Fatalf("missing coordinator = (%v, %v)", manager, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			guard := &testGuard{ctx: context.Background()}
			coordinator := &testCoordinator{guard: guard}
			manager, err = NewManager(logger, spec, tracingProvider, metricsProvider, coordinator)
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.cronJobs[0].job.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !ran {
				t.Fatal("injected task did not run")
			}
			if policy.distributed() && (coordinator.key != "injected" || guard.releases != 1) {
				t.Fatalf("injected coordinator was not used: %+v, %+v", coordinator, guard)
			}
		})
	}
}

func TestIntegrationJobCronOptionsAndMiddleware(t *testing.T) {
	observability := newRuntimeObservability(t)
	location := time.FixedZone("UTC+9", 9*60*60)
	var events []string
	middleware := func(next Handler) Handler {
		return func(ctx context.Context) error {
			events = append(events, "middleware:before")
			err := next(ctx)
			events = append(events, "middleware:after")
			return err
		}
	}
	run := make(chan struct{}, 1)
	spec := NewSpec().
		Middleware(nil, middleware).
		Option(
			nil,
			WithLocation(nil),
			WithLocation(location),
			WithTracing(false),
			WithMetrics(false),
			WithLogging(false),
		).
		RegisterCron("refresh", "@hourly", TaskFunc(func(context.Context) error {
			events = append(events, "task")
			run <- struct{}{}
			return nil
		}), RunImmediately(), WithConcurrentPolicy(DelayIfRunning)).(*Spec)
	manager, err := NewManager(
		observability.logger,
		spec,
		observability.tracingProvider,
		observability.metricsProvider,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if manager.options.Location != location || manager.options.TracingEnabled || manager.options.MetricsEnabled || manager.options.LoggingEnabled {
		t.Fatalf("manager options = %#v", manager.options)
	}
	startDone := make(chan error, 1)
	go func() { startDone <- manager.Start(context.Background()) }()
	waitFor(t, run)
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := waitForValue(t, startDone); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	want := []string{"middleware:before", "task", "middleware:after"}
	if len(events) != len(want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events = %#v, want %#v", events, want)
		}
	}
}

// TestIntegrationJobBatchErrors 验证真实构造链会运行全部单次任务，并保留多个失败原因。
func TestIntegrationJobBatchErrors(t *testing.T) {
	first, second := errors.New("invoice unavailable"), errors.New("report unavailable")
	completed := make(chan string, 3)
	spec := NewSpec()
	for _, item := range []struct {
		name string
		err  error
	}{{"invoice", first}, {"report", second}, {"healthy", nil}} {
		spec.RegisterOnce(item.name, TaskFunc(func(ctx context.Context) error {
			completed <- JobNameFromContext(ctx)
			return item.err
		}))
	}
	spec.ExitWhenDone()
	manager := newTestManager(t, spec)
	if !manager.HasJobs() || !manager.IsOneShot() {
		t.Fatal("batch lifecycle flags were lost")
	}
	err := manager.Start(context.Background())
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("batch error lost causes: %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for range 3 {
		seen[waitForValue(t, completed)] = true
	}
	if len(seen) != 3 || !seen["healthy"] || !seen["invoice"] || !seen["report"] {
		t.Fatalf("completed tasks = %v", seen)
	}
}

func TestIntegrationJobMixedLifecycle(t *testing.T) {
	for _, stopParent := range []bool{false, true} {
		t.Run(fmt.Sprintf("parent_cancel=%t", stopParent), func(t *testing.T) {
			started := make(chan string, 3)
			exited := make(chan string, 2)
			spec := NewSpec()
			spec.RegisterOnce("warmup", TaskFunc(func(context.Context) error { started <- "warmup"; return nil }))
			wait := TaskFunc(func(ctx context.Context) error {
				name := JobNameFromContext(ctx)
				started <- name
				<-ctx.Done()
				exited <- name
				return ctx.Err()
			})
			spec.RegisterDaemon("worker", wait)
			spec.RegisterCron("refresh", "@hourly", wait, RunImmediately())
			manager := newTestManager(t, spec)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			defer func() {
				if err := manager.Stop(context.Background()); err != nil {
					t.Error(err)
				}
			}()
			done := make(chan error, 1)
			go func() { done <- manager.Start(ctx) }()
			seen := make(map[string]bool)
			for range 3 {
				seen[waitForValue(t, started)] = true
			}
			if !seen["warmup"] || !seen["worker"] || !seen["refresh"] {
				t.Fatalf("mixed tasks = %v", seen)
			}
			// 使用任务入口信号确认全部启动，避免依赖调度时序或真实时间等待。
			if stopParent {
				cancel()
				if err := waitForValue(t, done); !errors.Is(err, context.Canceled) {
					t.Fatalf("parent cancellation = %v", err)
				}
			}
			if err := manager.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !stopParent {
				if err := waitForValue(t, done); err != nil {
					t.Fatal(err)
				}
			}
			if a, b := waitForValue(t, exited), waitForValue(t, exited); a == b {
				t.Fatalf("shutdown did not wait for both task types: %q %q", a, b)
			}
		})
	}
}

func TestIntegrationJobMiddlewareRejectsExecution(t *testing.T) {
	denied := errors.New("task is disabled by application policy")
	var name string
	spec := NewSpec()
	spec.Middleware(func(Handler) Handler {
		return func(ctx context.Context) error { name = JobNameFromContext(ctx); return denied }
	})
	spec.RegisterOnce("guarded", TaskFunc(func(context.Context) error {
		t.Error("middleware rejection still executed task")
		return nil
	})).ExitWhenDone()
	manager := newTestManager(t, spec)
	if err := manager.Start(context.Background()); !errors.Is(err, denied) {
		t.Fatalf("middleware rejection = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if name != "guarded" {
		t.Fatalf("middleware task context = %q", name)
	}
}
