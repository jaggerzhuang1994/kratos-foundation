package job

import (
	"context"
	"testing"
	"time"
)

func TestJobAdmissionMetricsProviderReportsAllInstruments(t *testing.T) {
	observability := newRuntimeObservability(t)
	metrics, err := newJobMetricsProvider(observability.metricsProvider, true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	metrics.reportSkipped(ctx, "cleanup", "pending_full")
	metrics.reportPending(ctx, "cleanup", 1)
	metrics.reportPending(ctx, "cleanup", -1)
	metrics.reportWait(ctx, "cleanup", "admitted", 25*time.Millisecond)

	if sample := findJobMetricSample(t, observability.metricsProvider, jobSkippedInstrumentName, map[string]string{
		metricLabelJob: "cleanup", metricLabelReason: "pending_full",
	}); sample.counter != 1 {
		t.Fatalf("skip counter = %v, want 1", sample.counter)
	}
	if sample := findJobMetricSample(t, observability.metricsProvider, jobPendingInstrumentName, map[string]string{
		metricLabelJob: "cleanup",
	}); sample.gauge != 0 {
		t.Fatalf("pending gauge = %v, want 0", sample.gauge)
	}
	if sample := findJobMetricSample(t, observability.metricsProvider, jobWaitInstrumentName, map[string]string{
		metricLabelJob: "cleanup", metricLabelResult: "admitted",
	}); sample.histogramCount != 1 || sample.histogramSum <= 0 {
		t.Fatalf("wait histogram = count %d sum %v", sample.histogramCount, sample.histogramSum)
	}
}
