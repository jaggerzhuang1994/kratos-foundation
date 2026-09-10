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
	manager, err := NewManager(logger, NewSpec(), tracingProvider, metricsProvider, nil)
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

type failingModuleLogger struct {
	log.Logger
	failAt int
	calls  int
	err    error
}

func (l *failingModuleLogger) WithModuleConfig(
	module string,
	config log.ModuleConfig,
) (log.Logger, error) {
	l.calls++
	if l.calls == l.failAt {
		return nil, l.err
	}
	return l.Logger.WithModuleConfig(module, config)
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

func TestNewManagerPropagatesModuleLoggerConfigurationFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		failAt int
		want   string
	}{
		{name: "job logger", failAt: 1, want: "configure job logger"},
		{name: "cron logger", failAt: 2, want: "configure cron logger"},
	} {
		t.Run(test.name, func(t *testing.T) {
			observability := newRuntimeObservability(t)
			wantErr := errors.New("invalid module configuration")
			logger := &failingModuleLogger{
				Logger: observability.logger,
				failAt: test.failAt,
				err:    wantErr,
			}
			spec := NewSpec().Option(WithLogging(false)).(*Spec)
			manager, err := NewManager(
				logger,
				spec,
				observability.tracingProvider,
				observability.metricsProvider,
				nil,
			)
			if manager != nil || !errors.Is(err, wantErr) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewManager(logger failure) = (%v, %v)", manager, err)
			}
			if logger.calls != test.failAt {
				t.Fatalf("module logger calls = %d, want %d", logger.calls, test.failAt)
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
