package job

import (
	"context"
	"time"

	foundationmetrics "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	foundationtracing "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const (
	instrumentationNameJob = "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	// metricLabelJobName 任务名标签。不用 job：它与 Prometheus 的采集任务标签冲突，抓取后会被改名为 exported_job。
	metricLabelJobName        = "job_name"
	metricLabelStatus         = "status"
	metricLabelReason         = "reason"
	metricLabelResult         = "result"
	jobRunsInstrumentName     = "job_runs_total"
	jobDurationInstrumentName = "job_duration_seconds"
	jobRunningInstrumentName  = "job_running"
	jobSkippedInstrumentName  = "job_triggers_skipped_total"
	jobPendingInstrumentName  = "job_pending"
	jobWaitInstrumentName     = "job_wait_duration_seconds"
)

type jobMetrics interface {
	jobAdmissionMetrics
	initJob(context.Context, string)
	reportStart(context.Context)
	reportDone(context.Context, error, time.Duration)
}

type jobAdmissionMetrics interface {
	reportSkipped(context.Context, string, string)
	reportPending(context.Context, string, int64)
	reportWait(context.Context, string, string, time.Duration)
}

type jobMetricsProvider struct {
	// disabled 是否跳过任务指标采集。
	disabled bool
	// jobRunsTotal 已结束的任务执行次数，按任务名及成功/失败状态累计。
	jobRunsTotal metric.Int64Counter
	// jobDurationSeconds 任务执行耗时直方图，单位秒。
	jobDurationSeconds metric.Float64Histogram
	// jobRunning 当前执行中的任务数量。
	jobRunning metric.Int64UpDownCounter
	// jobTriggersSkippedTotal 按受控原因累计未准入的周期触发。
	jobTriggersSkippedTotal metric.Int64Counter
	// jobPending 记录 Delay 策略当前等待准入的调用数。
	jobPending metric.Int64UpDownCounter
	// jobWaitDurationSeconds 记录等待结束时的排队时长。
	jobWaitDurationSeconds metric.Float64Histogram
}

func newJobMetricsProvider(provider foundationmetrics.Provider, enabled bool) (jobMetrics, error) {
	result := &jobMetricsProvider{disabled: true}
	if !enabled {
		return result, nil
	}
	result.disabled = false
	meter := provider.Meter(instrumentationNameJob)
	var err error
	result.jobRunsTotal, err = meter.Int64Counter(jobRunsInstrumentName, metric.WithUnit("{call}"))
	if err != nil {
		return nil, err
	}
	result.jobDurationSeconds, err = meter.Float64Histogram(
		jobDurationInstrumentName,
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(1, 2, 5, 10, 30, 60, 300, 600, 1800, 3600, 7200, 21600, 43200, 86400),
	)
	if err != nil {
		return nil, err
	}
	result.jobRunning, err = meter.Int64UpDownCounter(jobRunningInstrumentName)
	if err != nil {
		return nil, err
	}
	result.jobTriggersSkippedTotal, err = meter.Int64Counter(jobSkippedInstrumentName, metric.WithUnit("{call}"))
	if err != nil {
		return nil, err
	}
	result.jobPending, err = meter.Int64UpDownCounter(jobPendingInstrumentName, metric.WithUnit("{call}"))
	if err != nil {
		return nil, err
	}
	result.jobWaitDurationSeconds, err = meter.Float64Histogram(
		jobWaitInstrumentName,
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.001, 0.01, 0.1, 1, 5, 30, 60, 300, 600, 1800, 3600),
	)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// jobAttributes 返回任务名标签 job_name 及额外标签。
func jobAttributes(name string, extra ...attribute.KeyValue) metric.MeasurementOption {
	attributes := make([]attribute.KeyValue, 0, len(extra)+1)
	attributes = append(attributes, attribute.String(metricLabelJobName, name))
	return metric.WithAttributes(append(attributes, extra...)...)
}

// initJob 在注册时为已知标签组合写入 0：没有序列时面板显示无数据，increase(...) > 0 类告警也会漏掉第一次失败。
func (provider *jobMetricsProvider) initJob(ctx context.Context, name string) {
	if provider.disabled {
		return
	}
	for _, status := range []string{"success", "failure"} {
		provider.jobRunsTotal.Add(ctx, 0, jobAttributes(name, attribute.String(metricLabelStatus, status)))
	}
	provider.jobRunning.Add(ctx, 0, jobAttributes(name))
}

func (provider *jobMetricsProvider) reportSkipped(ctx context.Context, name, reason string) {
	if provider.disabled {
		return
	}
	provider.jobTriggersSkippedTotal.Add(ctx, 1, jobAttributes(name, attribute.String(metricLabelReason, reason)))
}

func (provider *jobMetricsProvider) reportPending(ctx context.Context, name string, delta int64) {
	if provider.disabled {
		return
	}
	provider.jobPending.Add(ctx, delta, jobAttributes(name))
}

func (provider *jobMetricsProvider) reportWait(ctx context.Context, name, result string, duration time.Duration) {
	if provider.disabled {
		return
	}
	provider.jobWaitDurationSeconds.Record(ctx, duration.Seconds(), jobAttributes(name, attribute.String(metricLabelResult, result)))
}

func (provider *jobMetricsProvider) reportStart(ctx context.Context) {
	if provider.disabled {
		return
	}
	name := JobNameFromContext(ctx)
	if name == "" {
		return
	}
	provider.jobRunning.Add(ctx, 1, jobAttributes(name))
}

func (provider *jobMetricsProvider) reportDone(ctx context.Context, err error, duration time.Duration) {
	if provider.disabled {
		return
	}
	name := JobNameFromContext(ctx)
	if name == "" {
		return
	}
	status := "success"
	if err != nil {
		status = "failure"
	}
	attributes := jobAttributes(name, attribute.String(metricLabelStatus, status))
	provider.jobRunsTotal.Add(ctx, 1, attributes)
	provider.jobDurationSeconds.Record(ctx, duration.Seconds(), attributes)
	provider.jobRunning.Add(ctx, -1, jobAttributes(name))
}

type jobTracing interface {
	recordStart(context.Context) (context.Context, trace.Span)
	recordEnd(trace.Span, error)
}

type jobTracingProvider struct {
	// tracer 为任务执行创建 span 的追踪器。
	tracer trace.Tracer
}

func newJobTracingProvider(provider foundationtracing.Provider, enabled bool) jobTracing {
	result := &jobTracingProvider{}
	if !enabled {
		result.tracer = noop.NewTracerProvider().Tracer(instrumentationNameJob)
	} else {
		// 全局关闭 tracing 时 Provider 只生成关联 ID、不采样不导出：每次执行仍有独立的 trace.id，
		// 任务日志和它发起的下游调用可以按 trace.id 关联。
		result.tracer = provider.Tracer(instrumentationNameJob)
	}
	return result
}

func (provider *jobTracingProvider) recordStart(ctx context.Context) (context.Context, trace.Span) {
	return provider.tracer.Start(
		ctx,
		JobNameFromContext(ctx),
		trace.WithSpanKind(trace.SpanKindInternal),
	)
}

func (*jobTracingProvider) recordEnd(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "OK")
	}
	span.End()
}
