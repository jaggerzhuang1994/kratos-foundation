package metrics

import (
	"context"
	"math"
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

func TestRuntimeCostMetricsExported(t *testing.T) {
	provider, cleanup, err := NewProvider(appinfo.New("runtime-cost"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	families, err := provider.PrometheusGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, family := range families {
		names[family.GetName()] = true
	}
	for _, name := range []string{"go_cpu_classes_gc_total_cpu_seconds_total", "go_sched_latencies_seconds", "go_goroutines", "go_gc_duration_seconds", "go_memstats_heap_alloc_bytes"} {
		if !names[name] {
			t.Errorf("runtime metric %s not exported", name)
		}
	}
}

func TestProviderSecondsHistogramBuckets(t *testing.T) {
	provider, cleanup, err := NewProvider(testAppInfo{name: "histogram-buckets"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	seconds := []float64{.0001, .00025, .0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 300, 600, 1800, 3600, 7200, 21600, 43200, 86400}
	type histogramCase struct {
		name   string
		unit   string
		advice []float64
		want   []float64
	}
	cases := []histogramCase{
		{"view_default_seconds", "s", nil, seconds},
		// 模拟 Job 的秒单位建议桶，保证统一 View 优先于 instrument 配置。
		{"view_job_seconds", "s", []float64{1, 2, 5, 10, 30, 60, 300, 86400}, seconds},
		{"view_default_milliseconds", "ms", nil, []float64{0, 5, 10, 25, 50, 75, 100, 250, 500, 750, 1000, 2500, 5000, 7500, 10000}},
		{"view_advised_milliseconds", "ms", []float64{1, 2, 5}, []float64{1, 2, 5}},
	}
	for _, tc := range cases {
		options := []metric.Float64HistogramOption{metric.WithUnit(tc.unit)}
		if tc.advice != nil {
			options = append(options, metric.WithExplicitBucketBoundaries(tc.advice...))
		}
		h, err := provider.Meter("bucket-regression").Float64Histogram(tc.name, options...)
		if err != nil {
			t.Fatal(err)
		}
		h.Record(context.Background(), .0002)
		if tc.unit == "s" {
			// 小时级 Job 仍需落入有限桶，不能全部丢进 +Inf。
			h.Record(context.Background(), 3601)
		}
	}
	// 注册原生秒单位 collector，确保它仍使用自己的桶定义。
	native := clientprometheus.NewHistogram(clientprometheus.HistogramOpts{Name: "native_seconds", Buckets: []float64{.001, .01, 1}})
	if err := provider.PrometheusRegisterer().Register(native); err != nil {
		t.Fatal(err)
	}
	native.Observe(.0002)
	cases = append(cases, histogramCase{name: "native_seconds", want: []float64{.001, .01, 1}})
	families, err := provider.PrometheusGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, family := range families {
				if family.GetName() != tc.name {
					continue
				}
				if len(family.Metric) != 1 {
					t.Fatalf("samples = %d, want 1", len(family.Metric))
				}
				h := family.Metric[0].GetHistogram()
				wantSamples, wantSum := uint64(1), .0002
				if tc.unit == "s" {
					wantSamples, wantSum = 2, 3601.0002
				}
				if h.GetSampleCount() != wantSamples || h.GetSampleSum() != wantSum {
					t.Fatalf("count/sum = %d/%g", h.GetSampleCount(), h.GetSampleSum())
				}
				finite := 0
				for _, bucket := range h.Bucket {
					upper := bucket.GetUpperBound()
					if math.IsInf(upper, 1) {
						continue
					}
					if finite >= len(tc.want) || upper != tc.want[finite] {
						t.Fatalf("bucket %d = %g, want %v", finite, upper, tc.want)
					}
					wantCount := uint64(0)
					if upper >= .0002 {
						wantCount = 1
					}
					if tc.unit == "s" && upper >= 3601 {
						wantCount++
					}
					if bucket.GetCumulativeCount() != wantCount {
						t.Errorf("bucket %g count = %d, want %d", upper, bucket.GetCumulativeCount(), wantCount)
					}
					finite++
				}
				if finite != len(tc.want) {
					t.Fatalf("finite buckets = %d, want %d", finite, len(tc.want))
				}
				return
			}
			t.Fatalf("histogram %s missing", tc.name)
		})
	}
}
