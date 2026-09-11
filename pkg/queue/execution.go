package queue

import (
	"context"
	"errors"
	"fmt"
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
	switch {
	case reservation.Attempts > w.retry.MaxAttempts:
		// 崩溃同样消耗领取次数，重启不能无限绕过最大尝试数。
		handlerErr = errors.New("queue attempts exhausted before execution")
	case handler == nil:
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
			w.log.WithContext(spanCtx).Debugw("function", "Worker", "event", "task.completed", "queue", w.config.Queue, "task.id", task.ID)
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
			w.log.WithContext(spanCtx).Errorw("function", "Worker", "event", "task.failed", "queue", w.config.Queue, "task.id", task.ID, "reason", reason, "attempts", reservation.Attempts)
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
			w.log.WithContext(spanCtx).Warnw("function", "Worker", "event", "retry.scheduled", "queue", w.config.Queue, "task.id", task.ID, "attempts", reservation.Attempts)
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

func invokeHandler(ctx context.Context, handler Handler, task *Task) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("queue handler panicked")
		}
	}()
	return handler(ctx, task.Clone())
}
