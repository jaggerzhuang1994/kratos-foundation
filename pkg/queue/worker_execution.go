package queue

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func (w *Worker[T]) execute(ctx context.Context, reservation *Reservation, claimedAt time.Time) error {
	if reservation.Task == nil || reservation.Token == "" || reservation.Attempts < 1 {
		return errInvalidReservation
	}
	task := reservation.Task
	parent := taskPropagator.Extract(ctx, propagation.MapCarrier(task.Headers))
	spanCtx, span := w.telemetry.Tracer().Start(parent, "queue.execute", trace.WithSpanKind(trace.SpanKindConsumer))
	defer span.End()
	started := time.Now()
	executionResult := "failure"
	logger := w.log.WithContext(spanCtx).With(
		"queue", w.config.Queue,
		"worker", w.config.Name,
		"task.id", task.ID,
		"task.message_version", task.MessageVersion,
		"attempts", reservation.Attempts,
	)
	// 每个领取都有指标与 span，逐次执行日志仅在 Debug 排障时输出。
	logger.Debugw("event", "queue.task.execution.started", "msg", "task execution started")
	defer func() {
		logger.Debugw(
			"event", "queue.task.execution.finished",
			"msg", "task execution finished",
			"result", executionResult,
			"duration", time.Since(started),
		)
	}()
	handler := w.handlers[task.MessageVersion]
	var handlerErr error
	cause := "handler_error"
	switch {
	case reservation.Attempts > w.retry.MaxAttempts:
		// 崩溃同样消耗领取次数，重启不能无限绕过最大尝试数。
		cause = "attempts_exhausted"
		handlerErr = errors.New("queue attempts exhausted before execution")
	case handler == nil:
		cause = "handler_missing"
		handlerErr = Permanent(errors.New("queue task message version is not registered"))
	default:
		handlerCtx, cancel := context.WithDeadline(spanCtx, claimedAt.Add(w.config.Timeout))
		if handlerCtx.Err() == nil {
			handlerCtx = context.WithValue(handlerCtx, executionMetadataKey{}, executionMetadata{attempt: reservation.Attempts, maxAttempts: w.retry.MaxAttempts})
			stopRenewal := w.keepLease(handlerCtx, reservation, func() { cancel() })
			handlerErr = invokeHandler(handlerCtx, handler, task)
			stopRenewal()
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
	} else if errors.Is(handlerErr, errMessageDecode) {
		cause = "decode_error"
	} else if errors.Is(handlerErr, errMessageValidation) {
		cause = "validation_error"
	}
	w.telemetry.RecordAttempt(spanCtx, span, w.config.Queue, w.config.Name, reservation.Attempts, handlerErr, time.Since(started))
	// 应用停止时不使用取消的上下文改写租约；由持久化租约超时恢复。
	if err := ctx.Err(); err != nil {
		executionResult = "stopped"
		return err
	}
	operationCtx, cancel := context.WithTimeout(spanCtx, w.config.StorageTimeout)
	defer cancel()
	result := "success"
	var err error
	switch {
	case handlerErr == nil:
		err = w.store.Ack(operationCtx, reservation)
	case IsPermanent(handlerErr) || reservation.Attempts >= w.retry.MaxAttempts:
		result = "failed"
		reason := "retry_exhausted"
		if IsPermanent(handlerErr) {
			reason = "permanent"
		}
		failedAt := time.Now().UTC()
		err = w.store.Fail(operationCtx, reservation, reason, failedAt)
		w.telemetry.RecordFinalClassification(span, reason, reservation.Attempts)
		failureResult := "success"
		if err != nil {
			failureResult = "error"
		}
		w.telemetry.RecordFailure(spanCtx, span, w.config.Queue, w.config.Name, failureResult)
		if err == nil {
			w.log.WithContext(spanCtx).Errorw("event", "queue.task.failed", "msg", "task failed", "queue", w.config.Queue, "worker", w.config.Name, "task.id", task.ID, "reason", reason, "error", cause, "task.message_version", task.MessageVersion, "attempts", reservation.Attempts)
			w.notifyFailure(spanCtx, FailureEvent{Queue: w.config.Queue, FailedTask: FailedTask{Task: task.Clone(), Attempts: reservation.Attempts, Reason: reason, FailedAt: failedAt}, Cause: cause, MaxAttempts: w.retry.MaxAttempts})
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
			w.log.WithContext(spanCtx).Warnw("event", "queue.task.retry_scheduled", "msg", "task retry scheduled", "queue", w.config.Queue, "worker", w.config.Name, "task.id", task.ID, "task.message_version", task.MessageVersion, "error", cause, "attempts", reservation.Attempts, "retry_after", delay)
		}
	}
	if err != nil {
		result = "storage_error"
	}
	executionResult = result
	if handlerErr != nil || err != nil {
		span.SetStatus(codes.Error, result)
	}
	w.telemetry.RecordMessage(spanCtx, w.config.Queue, w.config.Name, result, time.Since(started))
	return err
}

var errHandlerPanic = errors.New("queue handler panicked")

func invokeHandler(ctx context.Context, handler taskHandler, task *Task) (err error) {
	defer func() {
		if recover() != nil {
			err = errHandlerPanic
		}
	}()
	return handler(ctx, task.Clone())
}

// FailureEvent 表示已成功归档的最终失败；坏消息可能不能解码为业务类型。
// 普通回调不保证崩溃时必达，可靠通知须另用持久化事件或可靠任务。
type FailureEvent struct {
	// FailedTask 嵌入归档信息及独立任务副本。
	FailedTask
	// Queue 为失败任务所属逻辑队列。
	Queue string
	// Cause 为执行失败的受控分类，区别于存储层 Reason。
	Cause string
	// MaxAttempts 为该 Worker 的领取次数上限。
	MaxAttempts int
}

func (w *Worker[T]) notifyFailure(ctx context.Context, event FailureEvent) {
	if w.onFailed == nil {
		return
	}
	// 归档已经提交，回调失败不可再次执行消息或改写归档；另给协作式通知预算。
	ctx, cancel := context.WithTimeout(ctx, w.config.StorageTimeout)
	defer cancel()
	err := invokeFailure(ctx, w.onFailed, event)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		// 回调可能包含业务敏感数据，只记录受控分类和任务定位信息，不输出原始错误或 panic 值。
		reason := "error"
		switch {
		case errors.Is(err, errFailureCallbackPanicked):
			reason = "panic"
		case errors.Is(err, context.DeadlineExceeded):
			reason = "timeout"
		}
		w.log.WithContext(ctx).Errorw("event", "queue.failure.callback_failed", "msg", "task failure callback failed", "queue", event.Queue, "worker", w.config.Name, "task.id", event.Task.ID, "reason", reason)
	}
}

var errFailureCallbackPanicked = errors.New("queue failure callback panicked")

func invokeFailure(ctx context.Context, callback func(context.Context, FailureEvent) error, event FailureEvent) (err error) {
	defer func() {
		if recover() != nil {
			err = errFailureCallbackPanicked
		}
	}()
	return callback(ctx, event)
}

// keepLease 在 RenewLease 开启时每 Lease/3 续租一次，返回的函数停止续租并等待其退出。
// 租约已被他人取得时调用 lost 取消 Handler；其他存储错误只告警，下一周期再试。
func (w *Worker[T]) keepLease(ctx context.Context, reservation *Reservation, lost func()) func() {
	if w.renewer == nil {
		return func() {}
	}
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		ticker := time.NewTicker(w.config.Lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			renewCtx, cancel := context.WithTimeout(ctx, w.config.StorageTimeout)
			err := w.renewer.RenewLease(renewCtx, reservation, time.Now().UTC().Add(w.config.Lease))
			cancel()
			switch {
			case err == nil, ctx.Err() != nil:
			case errors.Is(err, ErrLeaseLost):
				w.log.WithContext(ctx).Warnw("event", "queue.lease.lost", "msg", "task lease lost; canceling handler", "queue", w.config.Queue, "worker", w.config.Name, "task.id", reservation.Task.ID)
				lost()
				return
			default:
				w.log.WithContext(ctx).Warnw("event", "queue.lease.renew_failed", "msg", "task lease renewal failed; retrying", "queue", w.config.Queue, "worker", w.config.Name, "task.id", reservation.Task.ID, "error", err)
			}
		}
	}()
	return func() {
		close(done)
		<-exited
	}
}
