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
		metricLabelJobName: "cleanup", metricLabelReason: "pending_full",
	}); sample.counter != 1 {
		t.Fatalf("skip counter = %v, want 1", sample.counter)
	}
	if sample := findJobMetricSample(t, observability.metricsProvider, jobPendingInstrumentName, map[string]string{
		metricLabelJobName: "cleanup",
	}); sample.gauge != 0 {
		t.Fatalf("pending gauge = %v, want 0", sample.gauge)
	}
	if sample := findJobMetricSample(t, observability.metricsProvider, jobWaitInstrumentName, map[string]string{
		metricLabelJobName: "cleanup", metricLabelResult: "admitted",
	}); sample.histogramCount != 1 || sample.histogramSum <= 0 {
		t.Fatalf("wait histogram = count %d sum %v", sample.histogramCount, sample.histogramSum)
	}
}

// 任务名只以 job_name 上报，不再带与 Prometheus 采集任务冲突的 job；注册时为执行结果与运行数写入 0。
func TestJobMetricsProviderInitializesKnownSeriesWithJobNameLabel(t *testing.T) {
	observability := newRuntimeObservability(t)
	metrics, err := newJobMetricsProvider(observability.metricsProvider, true)
	if err != nil {
		t.Fatal(err)
	}
	metrics.initJob(context.Background(), "cleanup")

	for _, status := range []string{"success", "failure"} {
		sample := findJobMetricSample(t, observability.metricsProvider, jobRunsInstrumentName, map[string]string{
			// 期望 job 为空串：带旧标签 job 的序列匹配不上，查找会失败。
			metricLabelJobName: "cleanup", "job": "", metricLabelStatus: status,
		})
		if sample.counter != 0 {
			t.Fatalf("initialized %s counter = %v, want 0", status, sample.counter)
		}
	}
	if sample := findJobMetricSample(t, observability.metricsProvider, jobRunningInstrumentName, map[string]string{
		metricLabelJobName: "cleanup",
	}); sample.gauge != 0 {
		t.Fatalf("initialized running gauge = %v, want 0", sample.gauge)
	}

	disabled, err := newJobMetricsProvider(observability.metricsProvider, false)
	if err != nil {
		t.Fatal(err)
	}
	disabled.initJob(context.Background(), "ignored")
}
