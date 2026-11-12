package queue

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"time"
	"unicode/utf8"

	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func (w *Worker[T]) execute(ctx context.Context, reservation *Reservation, claimedAt time.Time) error {
	if reservation.Task == nil || reservation.Token == "" || reservation.Attempts < 1 {
		return errInvalidReservation
	}
	task := reservation.Task
	// 一个任务一条 trace：trace.id 由任务 ID 确定，重试与首次执行共用，不沿用投递方的 trace。
	// 一次请求或定时任务可能批量投递，沿用投递方 trace 时这批任务及其下游调用的日志共用一个 trace.id，无法按任务区分。
	// 投递方 span 以 Link 关联，baggage 照常传播。
	producer := taskPropagator.Extract(ctx, propagation.MapCarrier(task.Headers))
	producerSpan := trace.SpanContextFromContext(producer)
	startOptions := []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindConsumer)}
	if producerSpan.IsValid() {
		startOptions = append(startOptions, trace.WithLinks(trace.Link{SpanContext: producerSpan}))
	}
	spanCtx, span := w.telemetry.Tracer().Start(
		trace.ContextWithRemoteSpanContext(producer, taskSpanContext(w.config.Queue, task.ID, producerSpan)),
		"queue.execute", startOptions...,
	)
	defer span.End()
	// Handler 内通过 Context 输出的业务日志同样带上任务定位字段；重试的执行另带 task.retry（第几次重试）。
	spanCtx = foundationlog.WithKv(spanCtx, "queue", w.config.Queue, "task.id", task.ID)
	if reservation.Attempts > 1 {
		spanCtx = foundationlog.WithKv(spanCtx, "task.retry", reservation.Attempts-1)
	}
	started := time.Now()
	executionResult := "failure"
	// queue 与 task.id 已随 spanCtx 带入。
	logger := w.log.WithContext(spanCtx).With(
		"worker", w.config.Name,
		"task.message_version", task.MessageVersion,
		"attempts", reservation.Attempts,
	)
	// 每次执行在 Info 记录开始与结束：结束日志带结果与耗时，相当于任务的访问日志；开始日志用于发现卡住未结束的任务。
	startedFields := []any{"event", "queue.task.execution.started", "msg", "task execution started"}
	if producerSpan.IsValid() {
		startedFields = append(startedFields, "dispatch.trace.id", producerSpan.TraceID().String())
	}
	logger.Infow(startedFields...)
	defer func() {
		logger.Infow(
			"event", "queue.task.execution.finished",
			"msg", "task execution finished",
			"result", executionResult,
			"duration", time.Since(started),
		)
	}()
	handler := w.handlers[task.MessageVersion]
	var handlerErr error
	// detailErr 只用于错误原文：执行 Context 到期时保留 Handler 自己返回的错误，分类仍以 handlerErr 为准。
	var detailErr error
	// interrupted 标记 Handler 因执行 Context 取消（排空超时或 Start 的 ctx 取消）而未完成。
	interrupted := false
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
		if ctxErr := handlerCtx.Err(); ctxErr != nil {
			detailErr = handlerErr
			if detailErr != nil && !errors.Is(detailErr, ctxErr) {
				detailErr = fmt.Errorf("%w: %v", ctxErr, detailErr)
			}
			handlerErr = ctxErr
		}
		interrupted = handlerErr != nil && ctx.Err() != nil
		cancel()
	}
	// cause 是框架可识别的受控分类；错误原文另记 error.detail 并由 ErrorRecorder 持久化。
	if errors.Is(handlerErr, context.DeadlineExceeded) {
		cause = "timeout"
	} else if errors.Is(handlerErr, errHandlerPanic) {
		cause = "panic"
		// panic 单独记一条：事件名以 .panic.recovered 结尾，不受日志级别限制并计入 panic 告警；
		// 随后的重试或失败日志只表达处理结果。
		logger.Errorw("event", "queue.task.panic.recovered", "msg", "task handler panic recovered", "error.detail", errorMessage(handlerErr))
	} else if errors.Is(handlerErr, errMessageDecode) {
		cause = "decode_error"
	} else if errors.Is(handlerErr, errMessageValidation) {
		cause = "validation_error"
	}
	w.telemetry.RecordAttempt(spanCtx, span, w.config.Queue, w.config.Name, reservation.Attempts, handlerErr, time.Since(started))
	// 存储操作脱离执行 Context 的取消：排空期间完成或被中止的任务都要在 StorageTimeout 内写回结果。
	operationCtx, cancel := context.WithTimeout(context.WithoutCancel(spanCtx), w.config.StorageTimeout)
	defer cancel()
	if interrupted {
		// 停机中止不是执行失败：立即释放供其他实例重新领取，不等租约到期，也不按失败退避。
		executionResult = "stopped"
		return w.store.Release(operationCtx, reservation, time.Now().UTC())
	}
	var executionError ExecutionError
	if handlerErr != nil {
		if detailErr == nil {
			detailErr = handlerErr
		}
		executionError = ExecutionError{Message: errorMessage(detailErr), At: time.Now().UTC()}
	}
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
		failedAt := executionError.At
		if w.recorder != nil {
			err = w.recorder.FailWithError(operationCtx, reservation, reason, failedAt, executionError)
		} else {
			err = w.store.Fail(operationCtx, reservation, reason, failedAt)
		}
		w.telemetry.RecordFinalClassification(span, reason, reservation.Attempts)
		failureResult := "success"
		if err != nil {
			failureResult = "error"
		}
		w.telemetry.RecordFailure(spanCtx, span, w.config.Queue, w.config.Name, failureResult)
		if err == nil {
			// logger 已绑定 queue、worker、task.id、task.message_version 与 attempts。
			logger.Errorw("event", "queue.task.failed", "msg", "task failed", "reason", reason, "error", cause, "error.detail", executionError.Message)
			w.notifyFailure(spanCtx, FailureEvent{Queue: w.config.Queue, FailedTask: FailedTask{Task: task.Clone(), Attempts: reservation.Attempts, Reason: reason, FailedAt: failedAt, LastError: executionError.Message, LastErrorAt: executionError.At}, Cause: cause, MaxAttempts: w.retry.MaxAttempts})
		}
	default:
		result = "retry"
		delay := w.retry.MinBackoff
		for attempt := 1; attempt < reservation.Attempts; attempt++ {
			delay = nextBackoff(delay, w.retry.MaxBackoff)
		}
		retryAt := executionError.At.Add(delay)
		if w.recorder != nil {
			err = w.recorder.ReleaseWithError(operationCtx, reservation, retryAt, executionError)
		} else {
			err = w.store.Release(operationCtx, reservation, retryAt)
		}
		if err == nil {
			w.telemetry.RecordRetry(spanCtx, span, w.config.Queue, w.config.Name, reservation.Attempts)
			logger.Warnw("event", "queue.task.retry_scheduled", "msg", "task retry scheduled", "error", cause, "error.detail", executionError.Message, "retry_after", delay)
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

// taskSpanContext 返回任务级的父 span 上下文，同一任务的每次执行都是它的子 span，因而共用 trace.id。
// 任务 ID 是 UUID 时 trace.id 就是去掉连字符的任务 ID，可直接用任务 ID 检索；其他 ID 取队列名与 ID 的摘要。
// 采样沿用投递方的决定，没有投递方 trace 时按采样处理；该父 span 只用于归组，不会被导出。
func taskSpanContext(queueName, taskID string, producer trace.SpanContext) trace.SpanContext {
	digest := sha256.Sum256([]byte(queueName + "\x00" + taskID))
	var traceID trace.TraceID
	if parsed, err := trace.TraceIDFromHex(strings.ReplaceAll(taskID, "-", "")); err == nil {
		traceID = parsed
	} else {
		copy(traceID[:], digest[:16])
	}
	var spanID trace.SpanID
	copy(spanID[:], digest[16:24])
	if !spanID.IsValid() {
		spanID[7] = 1
	}
	flags := trace.FlagsSampled
	if producer.IsValid() {
		flags = producer.TraceFlags()
	}
	return trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: flags, Remote: true})
}

var errHandlerPanic = errors.New("queue handler panicked")

// invokeHandler 把 panic 转为可分类的错误，错误原文带 panic 值与调用栈，便于从失败记录定位。
func invokeHandler(ctx context.Context, handler taskHandler, task *Task) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: %v\n%s", errHandlerPanic, recovered, debug.Stack())
		}
	}()
	return handler(ctx, task.Clone())
}

// errorMessage 返回合法 UTF-8 的错误原文，超过 MaxErrorMessageBytes 时按字符边界截断。
func errorMessage(err error) string {
	message := strings.ToValidUTF8(err.Error(), "\uFFFD")
	if len(message) <= MaxErrorMessageBytes {
		return message
	}
	const suffix = "...(truncated)"
	cut := MaxErrorMessageBytes - len(suffix)
	for cut > 0 && !utf8.RuneStart(message[cut]) {
		cut--
	}
	return message[:cut] + suffix
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
