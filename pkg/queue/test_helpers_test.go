package queue

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type recordingProducer struct {
	mu         sync.Mutex
	messages   []*Message
	publishErr error
}

func newRecordingProducer() *recordingProducer { return &recordingProducer{} }

func (p *recordingProducer) Publish(_ context.Context, message *Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, message.Clone())
	return p.publishErr
}

func (p *recordingProducer) PublishBatch(_ context.Context, messages []*Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, message := range messages {
		p.messages = append(p.messages, message.Clone())
	}
	return p.publishErr
}

func (p *recordingProducer) publishCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.messages)
}

func (p *recordingProducer) single() *Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.messages) != 1 {
		panic(fmt.Sprintf("recorded messages = %d, want 1", len(p.messages)))
	}
	return p.messages[0].Clone()
}

type testTracingProvider struct {
	tp       trace.TracerProvider
	disabled bool
}

func (p testTracingProvider) Disabled() bool { return p.disabled }

func (p testTracingProvider) TracerProvider() trace.TracerProvider { return p.tp }

func (p testTracingProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return p.tp.Tracer(name, options...)
}

func newTestObservability(t *testing.T) (Observability, *tracetest.InMemoryExporter) {
	t.Helper()
	spans := tracetest.NewInMemoryExporter()
	tp := tracesdk.NewTracerProvider(tracesdk.WithSyncer(spans))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return newObservability(t, testTracingProvider{tp: tp}), spans
}

func newDisabledTestObservability(t *testing.T) Observability {
	t.Helper()
	return newObservability(t, testTracingProvider{tp: noop.NewTracerProvider(), disabled: true})
}

func newMetricTestObservability(t *testing.T) Observability {
	t.Helper()
	return newDisabledTestObservability(t)
}

func newObservability(t *testing.T, provider tracing.Provider) Observability {
	t.Helper()
	metricsProvider, cleanup, err := metrics.NewProvider(appinfo.New("queue-test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return Observability{Metrics: metricsProvider, Tracing: provider, Logger: newRecordingLogger()}
}

func headerValue(headers []Header, key string) string {
	for _, header := range headers {
		if header.Key == key {
			return string(header.Value)
		}
	}
	return ""
}

func countSpan(exporter *tracetest.InMemoryExporter, name string) int {
	count := 0
	for _, span := range exporter.GetSpans() {
		if span.Name == name {
			count++
		}
	}
	return count
}

func gatheredQueueMetricLabels(
	t testing.TB,
	provider metrics.Provider,
	familyName string,
) map[string]string {
	t.Helper()
	families, err := provider.PrometheusGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != familyName || len(family.GetMetric()) == 0 {
			continue
		}
		labels := make(map[string]string, len(family.GetMetric()[0].GetLabel()))
		for _, label := range family.GetMetric()[0].GetLabel() {
			labels[label.GetName()] = label.GetValue()
		}
		return labels
	}
	t.Fatalf("metric family %q not gathered", familyName)
	return nil
}

type queueMetricSample struct {
	labels         map[string]string
	counter        float64
	histogramCount uint64
}

func gatheredQueueMetricSamples(
	t testing.TB,
	provider metrics.Provider,
	familyName string,
) []queueMetricSample {
	t.Helper()
	families, err := provider.PrometheusGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != familyName {
			continue
		}
		samples := make([]queueMetricSample, 0, len(family.GetMetric()))
		for _, value := range family.GetMetric() {
			labels := make(map[string]string, len(value.GetLabel()))
			for _, label := range value.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			samples = append(samples, queueMetricSample{
				labels:         labels,
				counter:        value.GetCounter().GetValue(),
				histogramCount: value.GetHistogram().GetSampleCount(),
			})
		}
		return samples
	}
	t.Fatalf("metric family %q not gathered", familyName)
	return nil
}

func findQueueMetricSample(
	t testing.TB,
	provider metrics.Provider,
	familyName string,
	wantLabels map[string]string,
) queueMetricSample {
	t.Helper()
	for _, sample := range gatheredQueueMetricSamples(t, provider, familyName) {
		matches := true
		for key, value := range wantLabels {
			if sample.labels[key] != value {
				matches = false
				break
			}
		}
		if matches {
			return sample
		}
	}
	t.Fatalf("metric family %q does not contain labels %#v", familyName, wantLabels)
	return queueMetricSample{}
}

type recordingLogger struct {
	mu       sync.Mutex
	written  []string
	levels   []kratoslog.Level
	contexts []context.Context
}

func newRecordingLogger() *recordingLogger { return &recordingLogger{} }

func (l *recordingLogger) Log(level kratoslog.Level, keyvals ...any) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.written = append(l.written, fmt.Sprint(keyvals...))
	l.levels = append(l.levels, level)
	return nil
}

func (l *recordingLogger) With(...any) log.Logger       { return l }
func (l *recordingLogger) WithModule(string) log.Logger { return l }
func (l *recordingLogger) WithModuleConfig(string, log.ModuleConfig) (log.Logger, error) {
	return l, nil
}
func (l *recordingLogger) WithContext(ctx context.Context) log.Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.contexts = append(l.contexts, ctx)
	return l
}
func (l *recordingLogger) WithCallerDepth(int) log.Logger      { return l }
func (l *recordingLogger) AddCallerDepth(...int) log.Logger    { return l }
func (l *recordingLogger) WithFilterKeys(...string) log.Logger { return l }
func (l *recordingLogger) Debug(a ...any)                      { _ = l.Log(kratoslog.LevelDebug, a...) }
func (l *recordingLogger) Debugf(format string, a ...any)      { l.Debug(fmt.Sprintf(format, a...)) }
func (l *recordingLogger) Debugw(keyvals ...any)               { _ = l.Log(kratoslog.LevelDebug, keyvals...) }
func (l *recordingLogger) Info(a ...any)                       { _ = l.Log(kratoslog.LevelInfo, a...) }
func (l *recordingLogger) Infof(format string, a ...any)       { l.Info(fmt.Sprintf(format, a...)) }
func (l *recordingLogger) Infow(keyvals ...any)                { _ = l.Log(kratoslog.LevelInfo, keyvals...) }
func (l *recordingLogger) Warn(a ...any)                       { _ = l.Log(kratoslog.LevelWarn, a...) }
func (l *recordingLogger) Warnf(format string, a ...any)       { l.Warn(fmt.Sprintf(format, a...)) }
func (l *recordingLogger) Warnw(keyvals ...any)                { _ = l.Log(kratoslog.LevelWarn, keyvals...) }
func (l *recordingLogger) Error(a ...any)                      { _ = l.Log(kratoslog.LevelError, a...) }
func (l *recordingLogger) Errorf(format string, a ...any)      { l.Error(fmt.Sprintf(format, a...)) }
func (l *recordingLogger) Errorw(keyvals ...any)               { _ = l.Log(kratoslog.LevelError, keyvals...) }
func (l *recordingLogger) Fatal(a ...any)                      { _ = l.Log(kratoslog.LevelFatal, a...) }
func (l *recordingLogger) Fatalf(format string, a ...any)      { l.Fatal(fmt.Sprintf(format, a...)) }
func (l *recordingLogger) Fatalw(keyvals ...any)               { _ = l.Log(kratoslog.LevelFatal, keyvals...) }

func (l *recordingLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.written, "\n")
}

func (l *recordingLogger) lastLevel() (kratoslog.Level, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.levels) == 0 {
		return 0, false
	}
	return l.levels[len(l.levels)-1], true
}

func (l *recordingLogger) lastContext() context.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.contexts) == 0 {
		return nil
	}
	return l.contexts[len(l.contexts)-1]
}

var _ log.Logger = (*recordingLogger)(nil)
