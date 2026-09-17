package tracing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"go.opentelemetry.io/otel/attribute"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.38.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type testAppInfo struct {
	name string
}

func (testAppInfo) ID() string { return "test-id" }

func (i testAppInfo) Name() string { return i.name }

func (testAppInfo) Version() string { return "v1.0.0" }

func (testAppInfo) Metadata() map[string]string { return map[string]string{"env": "test"} }

type testProvider struct {
	tp trace.TracerProvider
}

func (testProvider) Disabled() bool { return false }

func (p testProvider) TracerProvider() trace.TracerProvider { return p.tp }

func (p testProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return p.tp.Tracer(name, options...)
}

func TestNewProviderReturnsDisabledProviderFromConfig(t *testing.T) {
	disabled := true
	provider, cleanup, err := NewProvider(
		testconfig.New(t, "tracing", &config_pb.Tracing{Disable: &disabled}),
		testAppInfo{name: "orders"},
	)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	t.Cleanup(cleanup)
	if !provider.Disabled() {
		t.Fatal("NewProvider() returned an enabled provider for disabled config")
	}

	_, span := NewTracing(provider, testAppInfo{name: "orders"}).Start(
		context.Background(),
		"order.create",
	)
	defer span.End()
	if span.IsRecording() {
		t.Fatal("disabled provider returned a recording span")
	}
	spanContext := span.SpanContext()
	if !spanContext.TraceID().IsValid() || !spanContext.SpanID().IsValid() {
		t.Fatalf("disabled provider span context = %s/%s, want valid ids", spanContext.TraceID(), spanContext.SpanID())
	}
}

func TestNewBuildsDisabledAndEnabledProviders(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		disabled bool
	}{
		{name: "local is disabled", env: "local", disabled: true},
		{name: "production is enabled", env: "prod", disabled: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("APP_ENV", test.env)
			provider, cleanup, err := NewProvider(
				testconfig.Empty(t),
				configTestAppInfo{name: "orders"},
			)
			if err != nil {
				t.Fatalf("NewProvider() error = %v", err)
			}
			if provider.Disabled() != test.disabled {
				t.Fatalf("Disabled() = %v, want %v", provider.Disabled(), test.disabled)
			}
			if provider.TracerProvider() == nil {
				t.Fatal("TracerProvider() = nil")
			}
			if provider.Tracer("test.scope") == nil {
				t.Fatal("Tracer() = nil")
			}
			cleanup()
			cleanup()
		})
	}
}

func TestHotReloadSamplerAppliesUpdatesAndCancels(t *testing.T) {
	initial := tracingSamplerConfig(config_pb.Sampler_NEVER)
	manager := &tracingManagerStub{initial: initial}
	hot, cancel, err := foundationconfig.NewHotReloadValue[config_pb.Tracing](manager, "tracing", initial)
	if err != nil {
		t.Fatal(err)
	}
	sampler, err := newDynamicSampler(hot)
	if err != nil {
		t.Fatal(err)
	}
	manager.observer("tracing", tracingSamplerConfig(config_pb.Sampler_ALWAYS), nil)
	decision := sampler.ShouldSample(tracesdk.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       trace.TraceID{1},
	}).Decision
	if decision != tracesdk.RecordAndSample {
		t.Fatalf("updated sampler decision = %v, want %v", decision, tracesdk.RecordAndSample)
	}

	cancel()
	if manager.cancelCount != 1 {
		t.Fatalf("subscription cancel count = %d, want 1", manager.cancelCount)
	}
}

func TestHotReloadSamplerReturnsManagerError(t *testing.T) {
	wantErr := errors.New("subscribe failed")
	manager := &tracingManagerStub{subscribeErr: wantErr}
	if _, _, err := foundationconfig.NewHotReloadValue[config_pb.Tracing](manager, "tracing", tracingSamplerConfig(config_pb.Sampler_NEVER)); !errors.Is(err, wantErr) {
		t.Fatalf("NewHotReloadValue() error = %v, want %v", err, wantErr)
	}
}

func tracingSamplerConfig(sample config_pb.Sampler_Sample) *config_pb.Tracing {
	return &config_pb.Tracing{Sampler: &config_pb.Sampler{Sample: &sample}}
}

func boolPointer(value bool) *bool {
	return &value
}

type tracingManagerStub struct {
	initial      *config_pb.Tracing
	observer     foundationconfig.Observer
	subscribeErr error
	cancelCount  int
}

func (m *tracingManagerStub) Load(_ string, target any, defaults ...any) error {
	initial := m.initial
	if initial == nil && len(defaults) > 0 {
		initial = defaults[0].(*config_pb.Tracing)
	}
	if initial != nil {
		proto.Merge(target.(*config_pb.Tracing), initial)
	}
	return nil
}

func (m *tracingManagerStub) Subscribe(
	_ string,
	_ any,
	observer foundationconfig.Observer,
	_ ...any,
) (func(), error) {
	if m.subscribeErr != nil {
		return nil, m.subscribeErr
	}
	m.observer = observer
	return func() { m.cancelCount++ }, nil
}

func TestProviderTracerKeepsServiceIdentityOutOfInstrumentationScope(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tracingResource, err := newTracingResource(
		[]attribute.KeyValue{semconv.ServiceNameKey.String("orders")},
	)
	if err != nil {
		t.Fatal(err)
	}
	tp, cleanup := newTracerProvider(
		exporter,
		tracesdk.AlwaysSample(),
		tracingResource,
	)
	t.Cleanup(cleanup)
	provider := newProvider(tp)

	tracer := provider.Tracer(
		"github.com/example/orders/internal/application",
		trace.WithInstrumentationVersion("v1.2.3"),
	)
	_, span := tracer.Start(context.Background(), "order.create")
	span.End()
	flusher := tp.(interface {
		ForceFlush(context.Context) error
	})
	if err := flusher.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush() error = %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	scope := spans[0].InstrumentationScope
	if scope.Name != "github.com/example/orders/internal/application" {
		t.Fatalf("scope name = %q", scope.Name)
	}
	if scope.Version != "v1.2.3" {
		t.Fatalf("scope version = %q", scope.Version)
	}
	if scope.Attributes.Len() != 0 {
		t.Fatalf("scope attributes = %v, want none", scope.Attributes.ToSlice())
	}
	serviceName, ok := spans[0].Resource.Set().Value(semconv.ServiceNameKey)
	if !ok || serviceName.AsString() != "orders" {
		t.Fatalf("resource service.name = %q, %v", serviceName.AsString(), ok)
	}
}

func TestNewTracingResourcePreservesDefaultResourceAttributes(t *testing.T) {
	tracingResource, err := newTracingResource(
		[]attribute.KeyValue{semconv.ServiceNameKey.String("orders")},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []attribute.Key{
		semconv.TelemetrySDKLanguageKey,
		semconv.TelemetrySDKNameKey,
		semconv.TelemetrySDKVersionKey,
	} {
		value, ok := tracingResource.Set().Value(key)
		if !ok || value.AsString() == "" {
			t.Errorf("trace resource attribute %q is missing", key)
		}
	}
}

type tracingContract interface {
	Disabled() bool
	TracerProvider() trace.TracerProvider
	Tracer(string, ...trace.TracerOption) trace.Tracer
}

var _ tracingContract = (*provider)(nil)

var _ tracingContract = (*disabledProvider)(nil)

func TestNewExporterBuildsFromTracingConfig(t *testing.T) {
	compression := config_pb.Exporter_GZIP
	exporter, err := newExporter(&config_pb.Tracing{Exporter: &config_pb.Exporter{
		EndpointUrl: proto.String("http://localhost:4318/v1/traces"),
		Compression: &compression,
		Headers:     map[string]string{"authorization": "test-token"},
		Timeout:     durationpb.New(time.Second),
		Retry: &config_pb.Exporter_RetryConfig{
			Enabled:         proto.Bool(true),
			InitialInterval: durationpb.New(time.Second),
			MaxInterval:     durationpb.New(2 * time.Second),
			MaxElapsedTime:  durationpb.New(3 * time.Second),
		},
	}})
	if err != nil {
		t.Fatalf("newExporter() error = %v", err)
	}
	if exporter == nil {
		t.Fatal("newExporter() returned nil")
	}
	t.Cleanup(func() {
		if err := exporter.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	})
}
