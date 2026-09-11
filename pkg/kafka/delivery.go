package kafka

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	headerSourceDestination = "x-queue-source-destination"
	headerSourceConsumer    = "x-queue-source-consumer"
	headerOriginalID        = "x-queue-original-message-id"
	headerError             = "x-queue-error"
	headerAttempts          = "x-queue-attempts"
)

// handleDelivery 为一次驱动投递创建 consumer span，并以返回值精确定义 ACK 边界。
func (r *ConsumerRuntime) handleDelivery(ctx context.Context, delivery Delivery) error {
	started := time.Now()
	message := delivery.Message
	spanParent := ctx
	if message != nil {
		spanParent = messagePropagator.Extract(ctx, headerCarrier{message: message})
	}
	spanCtx, span := r.telemetry.Tracer().Start(
		spanParent,
		"kafka.consume",
		trace.WithSpanKind(trace.SpanKindConsumer),
	)
	defer span.End()
	span.SetAttributes(
		attribute.String("messaging.destination.name", r.config.Destination),
		attribute.String("messaging.consumer.group.name", r.config.Name),
	)
	if id := messageID(message); id != "" {
		span.SetAttributes(attribute.String("messaging.message.id", id))
	}

	result, err := r.processDelivery(spanCtx, span, delivery)
	r.telemetry.RecordMessage(
		spanCtx,
		r.config.Destination,
		r.config.Name,
		result,
		time.Since(started),
	)
	if err != nil && !errors.Is(err, context.Canceled) {
		span.SetStatus(codes.Error, "consume failed")
	}
	return err
}

func (r *ConsumerRuntime) processDelivery(
	ctx context.Context,
	span trace.Span,
	delivery Delivery,
) (string, error) {
	message := delivery.Message
	if message == nil {
		message = &Message{}
		if delivery.Err == nil {
			delivery.Err = errors.New("queue delivery message is nil")
		}
	}
	if delivery.Err != nil {
		return r.exhausted(
			ctx,
			span,
			message,
			Permanent(fmt.Errorf("decode queue delivery: %w", delivery.Err)),
			1,
		)
	}
	if err := validateHeaders(message.Headers); err != nil {
		return r.exhausted(
			ctx,
			span,
			message,
			Permanent(fmt.Errorf("invalid queue delivery: %w", err)),
			1,
		)
	}

	delay := r.retry.MinBackoff
	for attempt := 1; attempt <= r.retry.MaxAttempts; attempt++ {
		attemptStarted := time.Now()
		err := callHandler(ctx, r.handler, message)
		r.telemetry.RecordAttempt(
			ctx,
			span,
			r.config.Destination,
			r.config.Name,
			attempt,
			err,
			time.Since(attemptStarted),
		)
		if err == nil {
			return "success", nil
		}
		if err := consumerContextErr(ctx); err != nil {
			return "canceled", err
		}
		if IsPermanent(err) || attempt == r.retry.MaxAttempts {
			return r.exhausted(ctx, span, message, err, attempt)
		}

		r.telemetry.RecordRetry(ctx, span, r.config.Destination, r.config.Name, attempt)
		r.logRetry(ctx, message, attempt)
		if err := waitBackoff(ctx, delay); err != nil {
			return "canceled", err
		}
		delay = nextBackoff(delay, r.retry.MaxBackoff)
	}
	return "success", nil
}

// exhausted 处理最终失败：未配置死信时保留业务错误，否则发布经过重新准备的隔离副本。
func (r *ConsumerRuntime) exhausted(
	ctx context.Context,
	span trace.Span,
	message *Message,
	handlerErr error,
	attempts int,
) (string, error) {
	if err := consumerContextErr(ctx); err != nil {
		return "canceled", err
	}
	r.telemetry.RecordFinalClassification(span, failureReason(handlerErr), attempts)
	if r.config.DeadLetter == nil {
		r.logConsumeFailed(ctx, message, attempts, handlerErr)
		return "error", fmt.Errorf(
			"queue consumer %q exhausted after %d attempts: %w",
			r.config.Name,
			attempts,
			handlerErr,
		)
	}

	deadLetter := message.Clone()
	if deadLetter == nil {
		return "error", fmt.Errorf("queue consumer %q received a nil message", r.config.Name)
	}
	deadLetter.ID = ""
	deadLetter.Timestamp = time.Time{}
	deadLetter.Headers = removeInvalidHeaders(deadLetter.Headers)
	deadLetter.Headers = setHeader(deadLetter.Headers, headerSourceDestination, []byte(r.config.Destination))
	deadLetter.Headers = setHeader(deadLetter.Headers, headerSourceConsumer, []byte(r.config.Name))
	deadLetter.Headers = setHeader(deadLetter.Headers, headerOriginalID, []byte(messageID(message)))
	deadLetter.Headers = setHeader(deadLetter.Headers, headerError, []byte(failureReason(handlerErr)))
	deadLetter.Headers = setHeader(deadLetter.Headers, headerAttempts, []byte(strconv.Itoa(attempts)))
	prepared, err := prepareMessage(deadLetter)
	if err != nil {
		return "error", fmt.Errorf("prepare queue dead-letter message: %w", err)
	}
	if err := consumerContextErr(ctx); err != nil {
		return "canceled", err
	}
	if err := r.config.DeadLetter.Publish(ctx, prepared); err != nil {
		r.telemetry.RecordDeadLetter(ctx, span, r.config.Destination, r.config.Name, "error")
		r.logDeadLetterFailed(ctx, message, attempts)
		// 同时保留业务失败和投递失败，便于调用方用 errors.Is 检查任一根因。
		return "error", fmt.Errorf(
			"publish queue message %q to dead-letter destination %q: %w",
			messageID(message),
			r.config.DeadLetterDestination,
			errors.Join(handlerErr, err),
		)
	}
	r.telemetry.RecordDeadLetter(ctx, span, r.config.Destination, r.config.Name, "success")
	r.logDeadLettered(ctx, message, attempts)
	return "dead_lettered", nil
}

// failureReason 只暴露有限失败分类，避免将凭据等原始错误文本写入死信 Header。
func failureReason(err error) string {
	if IsPermanent(err) {
		return "permanent"
	}
	return "retry_exhausted"
}

// callHandler 向每次业务尝试传入独立深复制，并将 panic 转换为可重试错误。
func callHandler(ctx context.Context, handler Handler, message *Message) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("queue handler panic: %v\n%s", recovered, debug.Stack())
		}
	}()
	return handler(ctx, message.Clone())
}

// messageID 安全读取消息 ID，供错误路径处理 nil 投递。
func messageID(message *Message) string {
	if message == nil {
		return ""
	}
	return message.ID
}

type logLevel uint8

const (
	logDebug logLevel = iota
	logError
)

func (r *ConsumerRuntime) logEvent(ctx context.Context, level logLevel, event string) {
	if r.log == nil {
		return
	}
	logger := r.log.WithContext(ctx)
	fields := []any{
		"event", event,
		"kafka.destination", r.config.Destination,
		"kafka.consumer", r.config.Name,
	}
	if level == logError {
		logger.Errorw(fields...)
		return
	}
	logger.Debugw(fields...)
}

func (r *ConsumerRuntime) logRetry(ctx context.Context, message *Message, attempt int) {
	if r.log == nil {
		return
	}
	r.log.WithContext(ctx).Warnw(
		"event", "kafka.consume.retry",
		"kafka.destination", r.config.Destination,
		"kafka.consumer", r.config.Name,
		"message.id", messageID(message),
		"attempt", attempt,
		"kafka.error", "retryable",
	)
}

func (r *ConsumerRuntime) logConsumeFailed(ctx context.Context, message *Message, attempts int, err error) {
	if r.log == nil {
		return
	}
	r.log.WithContext(ctx).Errorw(
		"event", "kafka.consume.failed",
		"kafka.destination", r.config.Destination,
		"kafka.consumer", r.config.Name,
		"message.id", messageID(message),
		"attempts", attempts,
		"kafka.error", failureReason(err),
	)
}

func (r *ConsumerRuntime) logDeadLettered(ctx context.Context, message *Message, attempts int) {
	if r.log == nil {
		return
	}
	r.log.WithContext(ctx).Warnw(
		"event", "kafka.consume.dead_lettered",
		"kafka.destination", r.config.Destination,
		"kafka.consumer", r.config.Name,
		"kafka.dead_letter_destination", r.config.DeadLetterDestination,
		"message.id", messageID(message),
		"attempts", attempts,
	)
}

func (r *ConsumerRuntime) logDeadLetterFailed(ctx context.Context, message *Message, attempts int) {
	if r.log == nil {
		return
	}
	r.log.WithContext(ctx).Errorw(
		"event", "kafka.dead_letter.failed",
		"kafka.destination", r.config.Destination,
		"kafka.consumer", r.config.Name,
		"kafka.dead_letter_destination", r.config.DeadLetterDestination,
		"message.id", messageID(message),
		"attempts", attempts,
	)
}
