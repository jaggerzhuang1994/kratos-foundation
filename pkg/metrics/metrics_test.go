package metrics

import (
	"context"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	clientprometheus "github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type testAppInfo struct {
	name string
}

func (testAppInfo) ID() string                  { return "test-id" }
func (i testAppInfo) Name() string              { return i.name }
func (testAppInfo) Version() string             { return "v1.0.0" }
func (testAppInfo) Metadata() map[string]string { return map[string]string{"env": "test"} }

func TestNewMetricsReturnsMeterNamedAfterApp(t *testing.T) {
	appInfo := testAppInfo{name: "orders"}
	provider, cleanup, err := NewProvider(appInfo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	meter := NewMetrics(provider, appInfo)

	counter, err := meter.Int64Counter("business_events")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(context.Background(), 1)

	labels := gatheredScopeLabels(t, provider)
	if got := labels["otel_scope_name"]; got != "orders" {
		t.Fatalf("default meter scope name = %q, want orders", got)
	}
}

func TestNewMetricsDoesNotDuplicateServiceAttributesIntoScope(t *testing.T) {
	appInfo := testAppInfo{name: "orders"}
	provider, cleanup, err := NewProvider(appInfo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	counter, err := NewMetrics(provider, appInfo).Int64Counter("business_events")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(context.Background(), 1)

	labels := gatheredScopeLabels(t, provider)
	for _, name := range []string{
		"otel_scope_service_name",
		"otel_scope_service_instance_id",
		"otel_scope_service_version",
	} {
		if _, exists := labels[name]; exists {
			t.Errorf("default meter unexpectedly contains scope label %q", name)
		}
	}
}

func TestNewProviderPreservesDefaultResourceAttributes(t *testing.T) {
	provider, cleanup, err := NewProvider(testAppInfo{name: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	labels := gatheredMetricFamilyLabels(t, provider, "target_info")
	for _, name := range []string{
		"telemetry_sdk_language",
		"telemetry_sdk_name",
		"telemetry_sdk_version",
	} {
		if labels[name] == "" {
			t.Errorf("target_info resource label %q is missing", name)
		}
	}
}

func TestProviderMeterForwardsOptions(t *testing.T) {
	provider, cleanup, err := NewProvider(testAppInfo{name: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	meter := provider.Meter(
		"github.com/example/component",
		metric.WithInstrumentationVersion("v1.2.3"),
		metric.WithSchemaURL("https://example.com/schema/1.0"),
		metric.WithInstrumentationAttributes(attribute.String("component", "queue")),
	)
	counter, err := meter.Int64Counter("component_events")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(context.Background(), 1)

	labels := gatheredScopeLabels(t, provider)
	want := map[string]string{
		"otel_scope_name":       "github.com/example/component",
		"otel_scope_version":    "v1.2.3",
		"otel_scope_schema_url": "https://example.com/schema/1.0",
		"otel_scope_component":  "queue",
	}
	for name, value := range want {
		if got := labels[name]; got != value {
			t.Errorf("scope label %s = %q, want %q", name, got, value)
		}
	}
}

func gatheredScopeLabels(t testing.TB, provider Provider) map[string]string {
	t.Helper()
	families, err := provider.PrometheusGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, sample := range family.GetMetric() {
			labels := make(map[string]string, len(sample.GetLabel()))
			for _, label := range sample.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["otel_scope_name"] != "" {
				return labels
			}
		}
	}
	t.Fatal("no metric with OpenTelemetry scope labels gathered")
	return nil
}

func gatheredMetricFamilyLabels(
	t testing.TB,
	provider Provider,
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

type metricsContract interface {
	Meter(string, ...metric.MeterOption) metric.Meter
	MeterProvider() metric.MeterProvider
	PrometheusGatherer() clientprometheus.Gatherer
	PrometheusRegisterer() clientprometheus.Registerer
}

var _ metricsContract = (*provider)(nil)

func TestNewCreatesIsolatedProviderAndIdempotentCleanup(t *testing.T) {
	first, cleanupFirst, err := NewProvider(appinfo.New("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, cleanupSecond, err := NewProvider(appinfo.New("second"))
	if err != nil {
		cleanupFirst()
		t.Fatal(err)
	}
	t.Cleanup(cleanupFirst)
	t.Cleanup(cleanupSecond)

	if first.Meter("scope") == nil || first.MeterProvider() == nil {
		t.Fatal("provider returned nil OpenTelemetry components")
	}
	if first.PrometheusGatherer() == second.PrometheusGatherer() ||
		first.PrometheusRegisterer() == second.PrometheusRegisterer() {
		t.Fatal("independent providers share a Prometheus registry")
	}

	firstGauge := clientprometheus.NewGauge(clientprometheus.GaugeOpts{Name: "provider_isolation"})
	secondGauge := clientprometheus.NewGauge(clientprometheus.GaugeOpts{Name: "provider_isolation"})
	if err := first.PrometheusRegisterer().Register(firstGauge); err != nil {
		t.Fatal(err)
	}
	if err := second.PrometheusRegisterer().Register(secondGauge); err != nil {
		t.Fatalf("same collector name in an independent registry: %v", err)
	}
	if families, err := first.PrometheusGatherer().Gather(); err != nil || len(families) == 0 {
		t.Fatalf("Gather() = (%d families, %v)", len(families), err)
	}

	cleanupFirst()
	cleanupFirst()
}
