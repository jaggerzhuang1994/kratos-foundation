package queue

import (
	"context"
	"errors"
)

// FailureEvent 表示已成功归档的最终失败，包含独立任务副本。
// Cause 是受控分类，Reason 沿用 Store 分类；坏消息可能不能解码为业务类型。
// 普通回调不保证崩溃时必达，可靠通知须另用持久化事件或可靠任务。
type FailureEvent struct {
	FailedTask
	Queue       string
	Cause       string
	MaxAttempts int
}

func (w *Worker) notifyFailure(ctx context.Context, event FailureEvent) {
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
		// 回调可能包含业务敏感数据，只记录受控事件和任务定位信息。
		w.log.WithContext(ctx).Errorw("function", "notifyFailure", "event", "failure.callback_failed", "queue", event.Queue, "task.id", event.Task.ID)
	}
}

func invokeFailure(ctx context.Context, callback func(context.Context, FailureEvent) error, event FailureEvent) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("queue failure callback panicked")
		}
	}()
	return callback(ctx, event)
}
