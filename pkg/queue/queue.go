package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	internaltelemetry "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue/internal/telemetry"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var (
	errMessageDecode     = errors.New("queue message decode failed")
	errMessageValidation = errors.New("queue message validation failed")
)

// Codec 编解码一种业务消息；实现必须支持并发调用，返回的数据由调用方独立拥有。
type Codec[T any] interface {
	Encode(T) ([]byte, error)
	Decode([]byte) (T, error)
}

// JSONCodec 使用标准库 JSON 编解码消息。
type JSONCodec[T any] struct{}

// Encode 将消息编码为 JSON。
func (JSONCodec[T]) Encode(message T) ([]byte, error) { return json.Marshal(message) }

// Decode 将 JSON 解码为消息；业务字段约束由 Definition.Validate 校验。
func (JSONCodec[T]) Decode(data []byte) (T, error) {
	var message T
	err := json.Unmarshal(data, &message)
	return message, err
}

// Definition 定义一个队列的消息契约，发布与消费必须使用相同定义。
// MessageType 是显式稳定名称，Version 为正整数，持久化 Task.Type 为 MessageType.vVersion。
// Queue 是逻辑名称，Store 的物理队列仍由组装层绑定。Codec=nil 使用 JSON。
// 构造函数复制定义；Codec 和 Validate 被共享，必须并发安全且不能在运行期修改。
type Definition[T any] struct {
	Queue       string
	MessageType string
	Version     int
	Codec       Codec[T]
	Validate    func(T) error
}

func (d Definition[T]) resolve() (Definition[T], error) {
	if d.Queue == "" || strings.TrimSpace(d.Queue) != d.Queue || d.MessageType == "" || strings.TrimSpace(d.MessageType) != d.MessageType || d.Version < 1 {
		return d, errors.New("queue definition requires a queue, stable message type and positive version")
	}
	if d.Codec == nil {
		d.Codec = JSONCodec[T]{}
	}
	return d, nil
}

func (d Definition[T]) taskType() string { return fmt.Sprintf("%s.v%d", d.MessageType, d.Version) }

// PostOptions 设置稳定任务 ID、最早投递时间和业务 Header；零值表示即时投递并生成 ID。
// Header 不参与领取次数判定，调用期间不得并发修改输入。
type PostOptions struct {
	ID          string
	AvailableAt time.Time
	Headers     map[string]string
}

// Queue 是类型化投递入口，以消息类型区分依赖注入实例。
// Queue 不运行消费循环，不拥有 Store 或观测资源；通过 Worker 创建消费运行时。
type Queue[T any] struct {
	definition    Definition[T]
	dispatcher    *dispatcher
	store         Store
	observability Observability
}

// NewQueue 校验并固化消息定义，不访问 Store。
func NewQueue[T any](definition Definition[T], store Store, observability Observability) (*Queue[T], error) {
	definition, err := definition.resolve()
	if err != nil {
		return nil, err
	}
	dispatcher, err := newDispatcher(definition.Queue, store, observability)
	if err != nil {
		return nil, err
	}
	return &Queue[T]{definition: definition, dispatcher: dispatcher, store: store, observability: observability}, nil
}

// Post 校验并即时投递消息，返回任务 ID；事务内成功仅表示写入事务；提交后响应丢失仍可能返回错误。
func (q *Queue[T]) Post(ctx context.Context, message T) (string, error) {
	return q.PostWith(ctx, message, PostOptions{})
}

// PostWith 在编码前校验消息，失败时不访问 Store；不修改 options 中的 Header。
func (q *Queue[T]) PostWith(ctx context.Context, message T, options PostOptions) (string, error) {
	if q.definition.Validate != nil {
		if err := q.definition.Validate(message); err != nil {
			return "", fmt.Errorf("validate queue message: %w", err)
		}
	}
	payload, err := q.definition.Codec.Encode(message)
	if err != nil {
		return "", fmt.Errorf("encode queue message: %w", err)
	}
	return q.dispatcher.dispatch(ctx, &Task{ID: options.ID, Type: q.definition.taskType(), Payload: payload, Headers: options.Headers, AvailableAt: options.AvailableAt})
}

// dispatcher 向一个显式注入的队列存储投递任务，不持有连接生命周期。
type dispatcher struct {
	name      string
	store     Store
	log       log.Logger
	telemetry *internaltelemetry.Telemetry
}

var taskPropagator = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})

// newDispatcher 创建具备输入复制、校验及 trace 传播的任务投递入口。
// name 是观测使用的逻辑队列名，应描述 Store 绑定的队列，不用于选择 Repo 或数据库表。
func newDispatcher(name string, store Store, observability Observability) (*dispatcher, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("queue name is empty")
	}
	telemetry, err := internaltelemetry.New(observability.Tracing, observability.Metrics)
	if err != nil {
		return nil, fmt.Errorf("create queue dispatcher telemetry: %w", err)
	}
	return &dispatcher{name: name, store: store, log: observability.Logger.WithModule("queue"), telemetry: telemetry}, nil
}

// dispatch 投递即时或延迟任务，返回实际 ID，不修改输入。
// Database Store 参与业务事务时，成功仅代表写入该事务，最终以外层提交结果为准。
// AvailableAt 为零表示立即可领取；非零表示最早可领取时间，不保证准点执行。
// 网络错误可能发生在提交后；需要识别重复投递时，调用方应预先设置稳定 ID。
func (d *dispatcher) dispatch(ctx context.Context, task *Task) (string, error) {
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
