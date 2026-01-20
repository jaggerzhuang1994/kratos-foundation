package otel

import (
	"context"
	"time"

	jobcontext "github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/context"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/metrics"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	metricLabelJob    = "job"
	metricLabelStatus = "status"
)

type MetricsProvider interface {
	ReportStart(ctx context.Context)
	ReportDone(ctx context.Context, err error, duration time.Duration)
}

type metricsProvider struct {
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
	metrics metrics.Metrics,
	config Config,
) (MetricsProvider, error) {
	mp := &metricsProvider{
		disable: true,
	}
	if config.GetMetrics().GetDisable() {
		return mp, nil
	}
	mp.disable = false

	var meter = metrics.GetMeter()
	var err error

	mp.cronJobRunsTotal, err = meter.Int64Counter(
		"cron_job_runs_total",
		metric.WithUnit("{call}"),
	)
	if err != nil {
		return nil, err
	}

	mp.cronJobDurationSeconds, err = meter.Float64Histogram(
		"cron_job_duration_seconds",
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(1, 2, 5, 10, 30, 60, 300, 600, 1800, 3600, 7200, 21600, 43200, 86400),
	)
	if err != nil {
		return nil, err
	}

	mp.cronJobRunningGauge, err = meter.Int64Gauge(
		"cron_job_running",
	)
	if err != nil {
		return nil, err
	}

	return mp, nil
}

func (mp *metricsProvider) ReportStart(ctx context.Context) {
	if mp.disable {
		return
	}

	jobName := jobcontext.GetJobName(ctx)
	if jobName == "" {
		return
	}

	// 运行中任务数
	mp.cronJobRunningGauge.Record(ctx, 1,
		metric.WithAttributes(
			attribute.String(metricLabelJob, jobName),
		),
	)
}

func (mp *metricsProvider) ReportDone(ctx context.Context, err error, duration time.Duration) {
	if mp.disable {
		return
	}

	var status string
	if err == nil {
		status = "success"
	} else {
		status = "failure"
	}

	jobName := jobcontext.GetJobName(ctx)
	if jobName == "" {
		return
	}

	// 执行次数上报
	mp.cronJobRunsTotal.Add(
		ctx, 1,
		metric.WithAttributes(
			attribute.String(metricLabelJob, jobName),
			attribute.String(metricLabelStatus, status),
		),
	)

	// 耗时上报
	mp.cronJobDurationSeconds.Record(
		ctx, duration.Seconds(),
		metric.WithAttributes(
			attribute.String(metricLabelJob, jobName),
			attribute.String(metricLabelStatus, status),
		),
	)

	// 运行中任务数-1
	mp.cronJobRunningGauge.Record(ctx, -1,
		metric.WithAttributes(
			attribute.String(metricLabelJob, jobName),
		),
	)
}
