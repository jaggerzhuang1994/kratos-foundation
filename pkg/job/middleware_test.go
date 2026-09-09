package job

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	foundationmetrics "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"go.opentelemetry.io/otel/codes"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type runtimeObservability struct {
	logger          foundationlog.Logger
	log             moduleLog
	logPath         string
	metricsProvider foundationmetrics.Provider
	tracingProvider runtimeTracingProvider
	spans           *tracetest.InMemoryExporter
}

func newRuntimeObservability(t *testing.T) runtimeObservability {
	t.Helper()
	logger, logPath := testFileFoundationLogger(t)
	log, err := newJobLog(logger, managerOptions{LoggingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	metricsProvider, cleanupMetrics, err := foundationmetrics.NewProvider(appinfo.New("test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupMetrics)
	spans := tracetest.NewInMemoryExporter()
	tracerProvider := tracesdk.NewTracerProvider(tracesdk.WithSyncer(spans))
	t.Cleanup(func() {
		if err := tracerProvider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return runtimeObservability{
		logger:          logger,
		log:             log,
		logPath:         logPath,
		metricsProvider: metricsProvider,
		tracingProvider: runtimeTracingProvider{provider: tracerProvider},
		spans:           spans,
	}
}

type jobMetricSample struct {
	counter        float64
	histogramCount uint64
	histogramSum   float64
	gauge          float64
}

func findJobMetricSample(
	t testing.TB,
	provider foundationmetrics.Provider,
	familyName string,
	wantLabels map[string]string,
) jobMetricSample {
	t.Helper()
	families, err := provider.PrometheusGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != familyName {
			continue
		}
		for _, value := range family.GetMetric() {
			labels := make(map[string]string, len(value.GetLabel()))
			for _, label := range value.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			matched := true
			for key, want := range wantLabels {
				if labels[key] != want {
					matched = false
					break
				}
			}
			if matched {
				return jobMetricSample{
					counter:        value.GetCounter().GetValue(),
					histogramCount: value.GetHistogram().GetSampleCount(),
					histogramSum:   value.GetHistogram().GetSampleSum(),
					gauge:          value.GetGauge().GetValue(),
				}
			}
		}
	}
	t.Fatalf("metric family %q does not contain labels %#v", familyName, wantLabels)
	return jobMetricSample{}
}

func TestMiddlewaresReportSuccessFailureCancellationAndPanic(t *testing.T) {
	observability := newRuntimeObservability(t)
	middlewares, err := newMiddlewares(
		observability.log,
		managerOptions{TracingEnabled: true, MetricsEnabled: true, LoggingEnabled: true},
		observability.tracingProvider,
		observability.metricsProvider,
	)
	if err != nil {
		t.Fatal(err)
	}

	run := func(name string, ctx context.Context, handler Handler) error {
		return chainMiddlewares(middlewares...)(handler)(withJobName(ctx, name))
	}
	if err := run("success", context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	wantFailure := errors.New("failed")
	if err := run("failure", context.Background(), func(context.Context) error { return wantFailure }); !errors.Is(err, wantFailure) {
		t.Fatalf("failure error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run("canceled", canceled, func(ctx context.Context) error { return ctx.Err() }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
	panicErr := run("panic", context.Background(), func(context.Context) error { panic("broken invariant") })
	if panicErr == nil || !strings.Contains(panicErr.Error(), "job panic: broken invariant") || !strings.Contains(panicErr.Error(), "goroutine") {
		t.Fatalf("panic error = %v", panicErr)
	}

	wantSpanStatus := map[string]codes.Code{
		"success":  codes.Ok,
		"failure":  codes.Error,
		"canceled": codes.Error,
		"panic":    codes.Error,
	}
	if spans := observability.spans.GetSpans(); len(spans) != len(wantSpanStatus) {
		t.Fatalf("exported spans = %d, want %d", len(spans), len(wantSpanStatus))
	} else {
		for _, span := range spans {
			want, ok := wantSpanStatus[span.Name]
			if !ok || span.Status.Code != want {
				t.Errorf("span %q status = %v, want %v", span.Name, span.Status.Code, want)
			}
			if span.InstrumentationScope.Name != instrumentationNameJob {
				t.Errorf("span %q scope = %q", span.Name, span.InstrumentationScope.Name)
			}
		}
	}

	for _, outcome := range []struct {
		job, status string
	}{
		{job: "success", status: "success"},
		{job: "failure", status: "failure"},
		{job: "canceled", status: "failure"},
		{job: "panic", status: "failure"},
	} {
		labels := map[string]string{metricLabelJob: outcome.job, metricLabelStatus: outcome.status}
		if sample := findJobMetricSample(t, observability.metricsProvider, jobRunsInstrumentName, labels); sample.counter != 1 {
			t.Errorf("%s run counter = %v, want 1", outcome.job, sample.counter)
		}
		if sample := findJobMetricSample(t, observability.metricsProvider, jobDurationInstrumentName, labels); sample.histogramCount != 1 || sample.histogramSum < 0 {
			t.Errorf("%s duration = count %d sum %v", outcome.job, sample.histogramCount, sample.histogramSum)
		}
		if sample := findJobMetricSample(t, observability.metricsProvider, jobRunningInstrumentName, map[string]string{metricLabelJob: outcome.job}); sample.gauge != 0 {
			t.Errorf("%s running gauge = %v, want 0", outcome.job, sample.gauge)
		}
	}

	written, err := os.ReadFile(observability.logPath)
	if err != nil {
		t.Fatal(err)
	}
	logs := string(written)
	for _, fragment := range []string{
		"job execution started",
		"job execution done",
		"job execution failed",
		"job execution stopped",
		"job panic: broken invariant",
		"job=success",
		"job=failure",
		"job=canceled",
		"job=panic",
	} {
		if !strings.Contains(logs, fragment) {
			t.Errorf("job log lacks %q: %s", fragment, logs)
		}
	}
}

func TestDisabledObservabilityLeavesRecoveryActive(t *testing.T) {
	middlewares, err := newMiddlewares(
		testModuleLog(t),
		managerOptions{},
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(middlewares) != 1 {
		t.Fatalf("disabled middleware count = %d, want recovery only", len(middlewares))
	}
	err = chainMiddlewares(middlewares...)(func(context.Context) error { panic("still recovered") })(context.Background())
	if err == nil || !strings.Contains(err.Error(), "job panic: still recovered") {
		t.Fatalf("recovery error = %v", err)
	}

	metricsProvider, err := newJobMetricsProvider(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	metricsProvider.reportStart(context.Background())
	metricsProvider.reportDone(context.Background(), nil, 0)
}

func TestTelemetryIgnoresUnnamedJobsAndDisabledTracingProvider(t *testing.T) {
	observability := newRuntimeObservability(t)
	metricsProvider, err := newJobMetricsProvider(observability.metricsProvider, true)
	if err != nil {
		t.Fatal(err)
	}
	metricsProvider.reportStart(context.Background())
	metricsProvider.reportDone(context.Background(), errors.New("unnamed"), 0)
	families, err := observability.metricsProvider.PrometheusGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		switch family.GetName() {
		case jobRunsInstrumentName, jobDurationInstrumentName, jobRunningInstrumentName:
			if len(family.GetMetric()) != 0 {
				t.Fatalf("unnamed job emitted %s metrics: %#v", family.GetName(), family.GetMetric())
			}
		}
	}

	disabledTracing := newJobTracingProvider(runtimeTracingProvider{
		provider: observability.tracingProvider.provider,
		disabled: true,
	}, true)
	_, span := disabledTracing.recordStart(withJobName(context.Background(), "disabled"))
	if span.IsRecording() {
		t.Fatal("disabled tracing provider returned a recording span")
	}
	disabledTracing.recordEnd(span, errors.New("ignored"))
	if spans := observability.spans.GetSpans(); len(spans) != 0 {
		t.Fatalf("disabled tracing exported %d spans", len(spans))
	}
}
