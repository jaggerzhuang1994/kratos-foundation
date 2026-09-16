package job

import (
	"context"
	"errors"
)

// Task 表示可由运行时执行的任务。实现必须响应 ctx 取消，避免 Daemon 阻塞应用退出。
type Task interface {
	Run(ctx context.Context) error
}

// ScheduleProvider 为 Task 提供默认 Cron 表达式；注册时非空表达式和配置可覆盖它。
type ScheduleProvider interface{ Schedule() string }

// ConcurrentPolicyProvider 为 Task 提供默认本进程并发策略。
type ConcurrentPolicyProvider interface{ ConcurrentPolicy() ConcurrentPolicy }

// RunImmediatelyProvider 为 Task 提供默认的启动立即执行行为。
type RunImmediatelyProvider interface{ RunImmediately() bool }

// MaxPendingRunsProvider 为 Task 提供默认 Delay 等待容量。
type MaxPendingRunsProvider interface{ MaxPendingRuns() int }

// TaskFunc 把函数适配为 Task，适合无需额外状态的任务。
type TaskFunc func(ctx context.Context) error

// Run 执行被适配的函数。
func (f TaskFunc) Run(ctx context.Context) error { return f(ctx) }

// stoppedByContext 判断任务结果是否只是管理器主动取消产生的正常退出。
func stoppedByContext(ctx context.Context, err error) bool {
	if ctx.Err() == nil {
		return false
	}
	if err == nil {
		return true
	}
	// 聚合错误可能同时包含取消和业务失败；只有所有叶子都是取消才能忽略。
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		hasChild := false
		for _, child := range joined.Unwrap() {
			if child == nil {
				continue
			}
			hasChild = true
			if !stoppedByContext(ctx, child) {
				return false
			}
		}
		if hasChild {
			return true
		}
	}
	if child := errors.Unwrap(err); child != nil {
		return stoppedByContext(ctx, child)
	}
	return errors.Is(err, ctx.Err())
}

type managedJob struct {
	name string
	job  Task
}

// newManagedJob 固化任务名称和中间件链，使运行阶段不再重复组装。
func newManagedJob(name string, target Task, middlewares []Middleware) *managedJob {
	// recovery 只在最外层组装一次，覆盖并发控制、观测、业务中间件和 Task。
	// 观测层在 panic 展开时自行记录失败，由这里统一转换具体错误与堆栈。
	run := recoveryMiddleware()(chainMiddlewares(middlewares...)(target.Run))
	return &managedJob{
		name: name,
		job:  TaskFunc(run),
	}
}

// ErrCompleted 表示配置为完成后退出的单次任务已全部成功结束。
var ErrCompleted = errors.New("job completed")
