package otel

import (
	"context"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/config"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/job"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/metrics"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	metricLabelJob    = "job"
	metricLabelStatus = "status"
)

type MetricsProvider struct {
	disable bool

	// 任务执行次数 counter: cron_job_runs_total{job, status}
	cronJobRunsTotal metric.Int64Counter
	// 任务执行时长 histogram: cron_job_duration_seconds{job, status}
	// buckets(s): {1, 2, 5, 10, 30, 60, 300, 600, 1800, 3600, 7200, 21600, 43200, 86400}
	cronJobDurationSeconds metric.Float64Histogram
	// 运行任务数 gauge: cron_job_running{job}
	cronJobRunningGauge metric.Int64Gauge
}

func NewMetricsProvider(
	metrics *metrics.Metrics,
	config *config.Config,
) (mp *MetricsProvider, err error) {
	mp = &MetricsProvider{
		disable: true,
	}
	if config.GetMetrics().GetDisable() {
		return
	}
	mp.disable = false

	meter := metrics.GetMeter(config.GetMetrics().GetMeterName())

	mp.cronJobRunsTotal, err = meter.Int64Counter(
		"cron_job_runs_total",
		metric.WithUnit("{call}"),
	)
	if err != nil {
		return
	}

	mp.cronJobDurationSeconds, err = meter.Float64Histogram(
		"cron_job_duration_seconds",
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(1, 2, 5, 10, 30, 60, 300, 600, 1800, 3600, 7200, 21600, 43200, 86400),
	)
	if err != nil {
		return
	}

	mp.cronJobRunningGauge, err = meter.Int64Gauge(
		"cron_job_running",
	)
	if err != nil {
		return
	}

	return
}

func (mp *MetricsProvider) ReportStart(ctx context.Context) {
	if mp.disable {
		return
	}

	mp.cronJobRunningGauge.Record(ctx, 1,
		metric.WithAttributes(
			attribute.String(metricLabelJob, job.GetName(ctx)),
		),
	)
}

func (mp *MetricsProvider) ReportDone(ctx context.Context, err error, duration time.Duration) {
	if mp.disable {
		return
	}

	var status string
	if err == nil {
		status = "success"
	} else {
		status = "failure"
	}

	jobName := job.GetName(ctx)

	mp.cronJobRunsTotal.Add(
		ctx, 1,
		metric.WithAttributes(
			attribute.String(metricLabelJob, jobName),
			attribute.String(metricLabelStatus, status),
		),
	)

	mp.cronJobDurationSeconds.Record(
		ctx, duration.Seconds(),
		metric.WithAttributes(
			attribute.String(metricLabelJob, jobName),
			attribute.String(metricLabelStatus, status),
		),
	)

	// 运行任务数-1
	mp.cronJobRunningGauge.Record(ctx, -1,
		metric.WithAttributes(
			attribute.String(metricLabelJob, jobName),
		),
	)
}
