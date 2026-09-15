package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	internaltelemetry "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue/internal/telemetry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Observability 是任务投递与执行所需的日志、追踪和指标依赖。
type Observability struct {
	Logger  log.Logger
	Tracing tracing.Provider
	Metrics metrics.Provider
}

// Dispatcher 向一个显式注入的队列存储投递任务，不持有连接生命周期。
type Dispatcher struct {
	name      string
	store     Store
	log       log.Logger
	telemetry *internaltelemetry.Telemetry
}

var taskPropagator = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})

// NewDispatcher 创建具备输入复制、校验及 trace 传播的任务投递入口。
// name 是观测使用的逻辑队列名，应描述 Store 绑定的队列，不用于选择 Repo 或数据库表。
func NewDispatcher(name string, store Store, observability Observability) (*Dispatcher, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("queue name is empty")
	}
	telemetry, err := internaltelemetry.New(observability.Tracing, observability.Metrics)
	if err != nil {
		return nil, fmt.Errorf("create queue dispatcher telemetry: %w", err)
	}
	return &Dispatcher{name: name, store: store, log: observability.Logger.WithModule("queue"), telemetry: telemetry}, nil
}

// Dispatch 投递即时或延迟任务，返回实际 ID，不修改输入。
// Database Store 参与业务事务时，成功仅代表写入该事务，最终以外层提交结果为准。
// AvailableAt 为零表示立即可领取；非零表示最早可领取时间，不保证准点执行。
// 网络错误可能发生在提交后；需要识别重复投递时，调用方应预先设置稳定 ID。
func (d *Dispatcher) Dispatch(ctx context.Context, task *Task) (string, error) {
	started := time.Now()
	prepared, err := prepareTask(task, started.UTC())
	if err != nil {
		return "", err
	}
	spanCtx, span := d.telemetry.Tracer().Start(ctx, "queue.dispatch", trace.WithSpanKind(trace.SpanKindProducer))
	defer span.End()
	taskPropagator.Inject(spanCtx, propagation.MapCarrier(prepared.Headers))
	err = d.store.Enqueue(spanCtx, prepared)
	result := "success"
	if err != nil {
		result = "error"
		span.SetStatus(codes.Error, "enqueue failed")
		d.log.WithContext(spanCtx).Errorw("event", "enqueue.failed", "queue", d.name, "task.id", prepared.ID)
	}
	d.telemetry.RecordProducer(spanCtx, d.name, "dispatch", result, 1, time.Since(started))
	if err != nil {
		return prepared.ID, fmt.Errorf("enqueue task: %w", err)
	}
	return prepared.ID, nil
}

func prepareTask(task *Task, now time.Time) (*Task, error) {
	if task == nil {
		return nil, errors.New("queue task is nil")
	}
	task = task.Clone()
	task.Type = strings.TrimSpace(task.Type)
	if task.Type == "" {
		return nil, errors.New("queue task type is empty")
	}
	if task.ID == "" {
		task.ID = uuid.NewString()
	}
	if len(task.ID) > 128 || strings.TrimSpace(task.ID) == "" {
		return nil, errors.New("queue task id must contain 1 to 128 bytes")
	}
	for key := range task.Headers {
		if strings.TrimSpace(key) == "" {
			return nil, errors.New("queue task header key is empty")
		}
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.AvailableAt.IsZero() {
		task.AvailableAt = now
	}
	if task.Headers == nil {
		task.Headers = make(map[string]string)
	}
	return task, nil
}
