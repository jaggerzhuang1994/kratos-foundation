package job

import (
	"context"
)

// Job 业务层定义的执行器接口
type Job interface {
	Run(ctx context.Context) error
}

// FuncJob is a wrapper that turns a func(context.Context) into a Job
type FuncJob func(ctx context.Context) error

func (f FuncJob) Run(ctx context.Context) error { return f(ctx) }

func Run(ctx context.Context, name string, runner Job) error {
	ctx = WithName(ctx, name)
	return runner.Run(ctx)
}
