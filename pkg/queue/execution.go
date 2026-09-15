package queue

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func (w *Worker) execute(ctx context.Context, reservation *Reservation, claimedAt time.Time) error {
	if reservation.Task == nil || reservation.Token == "" || reservation.Attempts < 1 {
		return errors.New("queue store returned an invalid reservation")
	}
	task := reservation.Task
	parent := taskPropagator.Extract(ctx, propagation.MapCarrier(task.Headers))
	spanCtx, span := w.telemetry.Tracer().Start(parent, "queue.execute", trace.WithSpanKind(trace.SpanKindConsumer))
	defer span.End()
	started := time.Now()
	handler := w.handlers[task.Type]
	var handlerErr error
	cause := "handler_error"
	switch {
	case reservation.Attempts > w.retry.MaxAttempts:
		// 崩溃同样消耗领取次数，重启不能无限绕过最大尝试数。
		cause = "attempts_exhausted"
		handlerErr = errors.New("queue attempts exhausted before execution")
	case handler == nil:
		cause = "handler_missing"
		handlerErr = Permanent(errors.New("queue task type is not registered"))
	default:
		handlerCtx, cancel := context.WithDeadline(spanCtx, claimedAt.Add(w.config.Timeout))
		if handlerCtx.Err() == nil {
			handlerErr = invokeHandler(handlerCtx, handler, task)
		}
		if handlerCtx.Err() != nil {
			handlerErr = handlerCtx.Err()
		}
		cancel()
	}
	// 分类只包含框架可识别的原因，不输出 Handler 错误原文。
	if errors.Is(handlerErr, context.DeadlineExceeded) {
		cause = "timeout"
	} else if errors.Is(handlerErr, errHandlerPanic) {
		cause = "panic"
	}
	w.telemetry.RecordAttempt(spanCtx, span, w.config.Queue, w.config.Name, reservation.Attempts, handlerErr, time.Since(started))
	// 应用停止时不使用取消的上下文改写租约；由持久化租约超时恢复。
	if err := ctx.Err(); err != nil {
		return err
	}
	operationCtx, cancel := context.WithTimeout(spanCtx, w.config.StorageTimeout)
	defer cancel()
	result := "success"
	var err error
	switch {
	case handlerErr == nil:
		err = w.store.Ack(operationCtx, reservation)
		if err == nil {
			w.log.WithContext(spanCtx).Debugw("event", "task.completed", "queue", w.config.Queue, "task.id", task.ID)
		}
	case IsPermanent(handlerErr) || reservation.Attempts >= w.retry.MaxAttempts:
		result = "failed"
		reason := "retry_exhausted"
		if IsPermanent(handlerErr) {
			reason = "permanent"
		}
		err = w.store.Fail(operationCtx, reservation, reason, time.Now().UTC())
		w.telemetry.RecordFinalClassification(span, reason, reservation.Attempts)
		failureResult := "success"
		if err != nil {
			failureResult = "error"
		}
		w.telemetry.RecordFailure(spanCtx, span, w.config.Queue, w.config.Name, failureResult)
		if err == nil {
			w.log.WithContext(spanCtx).Errorw("event", "task.failed", "queue", w.config.Queue, "task.id", task.ID, "reason", reason, "cause", cause, "task.type", task.Type, "attempts", reservation.Attempts)
		}
	default:
		result = "retry"
		delay := w.retry.MinBackoff
		for attempt := 1; attempt < reservation.Attempts; attempt++ {
			delay = nextBackoff(delay, w.retry.MaxBackoff)
		}
		err = w.store.Release(operationCtx, reservation, time.Now().UTC().Add(delay))
		if err == nil {
			w.telemetry.RecordRetry(spanCtx, span, w.config.Queue, w.config.Name, reservation.Attempts)
			w.log.WithContext(spanCtx).Warnw("event", "retry.scheduled", "queue", w.config.Queue, "task.id", task.ID, "task.type", task.Type, "cause", cause, "attempts", reservation.Attempts, "retry_after", delay)
		}
	}
	if err != nil {
		result = "storage_error"
	}
	if handlerErr != nil || err != nil {
		span.SetStatus(codes.Error, result)
	}
	w.telemetry.RecordMessage(spanCtx, w.config.Queue, w.config.Name, result, time.Since(started))
	return err
}

var errHandlerPanic = errors.New("queue handler panicked")

func invokeHandler(ctx context.Context, handler Handler, task *Task) (err error) {
	defer func() {
		if recover() != nil {
			err = errHandlerPanic
		}
	}()
	return handler(ctx, task.Clone())
}
