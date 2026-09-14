package kafka

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	internaltelemetry "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka/internal/telemetry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var messagePropagator = propagation.NewCompositeTextMapPropagator(
	propagation.TraceContext{},
	propagation.Baggage{},
)

// Observability 定义 Producer 使用的日志、追踪和指标能力。
type Observability struct {
	Logger  log.Logger
	Tracing tracing.Provider
	Metrics metrics.Provider
}

// NewManagedProducer 将外部 Producer 包装为保留消息所有权且具备统一观测能力的 Producer。
func NewManagedProducer(name string, producer Producer, observability Observability) (Producer, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("queue producer destination is empty")
	}
	telemetry, err := internaltelemetry.New(observability.Tracing, observability.Metrics)
	if err != nil {
		return nil, fmt.Errorf("create queue producer telemetry: %w", err)
	}
	return newManagedProducer(name, producer, observability.Logger, telemetry), nil
}

type managedProducer struct {
	Producer
	destination string
	log         log.Logger
	telemetry   *internaltelemetry.Telemetry
}

// newManagedProducer 复用已创建的观测对象，避免 Manager 兼容路径重复构建指标或递归包装。
func newManagedProducer(
	destination string,
	producer Producer,
	logger log.Logger,
	telemetry *internaltelemetry.Telemetry,
) Producer {
	return &managedProducer{
		Producer:    producer,
		destination: destination,
		log:         logger.WithModule("kafka"),
		telemetry:   telemetry,
	}
}

// Publish 在转交驱动前复制消息、补齐元数据并注入传播上下文。
func (p *managedProducer) Publish(ctx context.Context, message *Message) error {
	started := time.Now()
	prepared, err := prepareMessage(message)
	if err != nil {
		p.rejected(ctx, "publish", started)
		return err
	}
	return p.publish(ctx, "publish", started, 1, func(publishCtx context.Context) error {
		messagePropagator.Inject(publishCtx, headerCarrier{message: prepared})
		return p.Producer.Publish(publishCtx, prepared)
	})
}

// PublishBatch 先完整准备所有消息，确保无效输入不会导致驱动收到部分批次。
func (p *managedProducer) PublishBatch(ctx context.Context, messages []*Message) error {
	if len(messages) == 0 {
		return nil
	}
	started := time.Now()
	prepared := make([]*Message, len(messages))
	for index, message := range messages {
		next, err := prepareMessage(message)
		if err != nil {
			p.rejected(ctx, "publish_batch", started)
			return fmt.Errorf("prepare queue message %d: %w", index, err)
		}
		prepared[index] = next
	}
	return p.publish(ctx, "publish_batch", started, int64(len(prepared)), func(publishCtx context.Context) error {
		for _, message := range prepared {
			messagePropagator.Inject(publishCtx, headerCarrier{message: message})
		}
		return p.Producer.PublishBatch(publishCtx, prepared)
	})
}

func (p *managedProducer) publish(
	ctx context.Context,
	operation string,
	started time.Time,
	count int64,
	publish func(context.Context) error,
) error {
	spanCtx, span := p.telemetry.Tracer().Start(ctx, "kafka.publish", trace.WithSpanKind(trace.SpanKindProducer))
	defer span.End()
	span.SetAttributes(
		attribute.String("messaging.destination.name", p.destination),
		attribute.String("messaging.operation.name", operation),
	)
	err := publish(spanCtx)
	result := "success"
	if err != nil {
		result = "error"
		span.SetStatus(codes.Error, "publish failed")
		p.failed(spanCtx)
	}
	p.record(ctx, operation, result, count, time.Since(started))
	if err != nil {
		return fmt.Errorf("publish queue message: %w", err)
	}
	return nil
}

func (p *managedProducer) rejected(ctx context.Context, operation string, started time.Time) {
	p.record(ctx, operation, "rejected", 1, time.Since(started))
	if p.log != nil {
		p.log.WithContext(ctx).Warnw(
			"event", "kafka.publish.rejected",
			"kafka.destination", p.destination,
		)
	}
}

func (p *managedProducer) failed(ctx context.Context) {
	if p.log != nil {
		p.log.WithContext(ctx).Errorw(
			"event", "kafka.publish.failed",
			"kafka.destination", p.destination,
		)
	}
}

func (p *managedProducer) record(
	ctx context.Context,
	operation string,
	result string,
	count int64,
	duration time.Duration,
) {
	p.telemetry.RecordProducer(ctx, p.destination, operation, result, count, duration)
}

// prepareMessage 复制并补齐驱动投递所需的稳定元数据，保留调用方对原对象的所有权。
func prepareMessage(message *Message) (*Message, error) {
	if message == nil {
		return nil, errors.New("queue message is nil")
	}
	prepared := message.Clone()
	if err := validateHeaders(prepared.Headers); err != nil {
		return nil, err
	}
	if prepared.ID == "" {
		prepared.ID = uuid.NewString()
	}
	if prepared.Timestamp.IsZero() {
		prepared.Timestamp = time.Now().UTC()
	}
	return prepared, nil
}
