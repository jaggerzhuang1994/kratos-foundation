package utils

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"
)

// Parallel 使用后台 context 并行执行任务，等待全部任务返回，并返回首个错误。
func Parallel(fns ...func(ctx2 context.Context) (err error)) error {
	return ParallelWithContext(context.Background(), fns...)
}

// ParallelWithContext 并行执行任务；首个错误会取消派生 context，但仍等待全部任务返回。
func ParallelWithContext(ctx context.Context, fns ...func(ctx2 context.Context) (err error)) error {
	// 任务需响应取消并自行返回；Wait 不会强制终止忽略 context 的任务。
	g, ctx2 := errgroup.WithContext(ctx)
	for i := range fns {
		fn := fns[i]
		g.Go(func() error {
			return fn(ctx2)
		})
	}
	return g.Wait()
}

// ParallelWithLimit 使用单次调用内的固定 worker 池，最多同时执行 workers 个任务。
// workers 必须为正数，任务必须非 nil；首个错误或取消会停止分发，已运行任务需自行响应取消。
// 函数等待所有 worker 退出，不恢复任务 panic，不为业务共享数据提供同步。
func ParallelWithLimit(ctx context.Context, workers int, fns ...func(context.Context) error) error {
	if workers <= 0 {
		return fmt.Errorf("parallel workers must be positive")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	g, workerCtx := errgroup.WithContext(ctx)
	// 无缓冲队列对分发施加背压；当前调用独占关闭权，worker 只接收任务。
	tasks := make(chan func(context.Context) error)
	for range min(workers, len(fns)) {
		g.Go(func() error {
			for {
				select {
				case <-workerCtx.Done():
					return workerCtx.Err()
				case fn, ok := <-tasks:
					if !ok {
						return nil
					}
					// 取消与接收可能同时就绪，在执行前再检查，避免启动已观察到取消的任务。
					if err := workerCtx.Err(); err != nil {
						return err
					}
					if err := fn(workerCtx); err != nil {
						return err
					}
				}
			}
		})
	}
dispatch:
	for _, fn := range fns {
		select {
		case <-workerCtx.Done():
			break dispatch
		case tasks <- fn:
		}
	}
	close(tasks)
	// 先保留任务的首个错误，再报告父 context；派生 context 会在 Wait 后自动取消。
	if err := g.Wait(); err != nil {
		return err
	}
	return ctx.Err()
}

// ParallelMap 使用 ParallelWithLimit 的 worker 池映射输入，按输入顺序返回结果。
// workers 必须为正数；空输入成功时返回非 nil 空切片，失败或取消时返回 nil 和错误。
// 不回滚已经执行的回调副作用；输入及其引用数据不得被回调无同步地并发修改。
func ParallelMap[T, Y any](ctx context.Context, workers int, input []T, transform func(context.Context, T) (Y, error)) ([]Y, error) {
	results := make([]Y, len(input))
	tasks := make([]func(context.Context) error, len(input))
	for i, value := range input {
		tasks[i] = func(taskCtx context.Context) error {
			result, err := transform(taskCtx, value)
			if err != nil {
				return fmt.Errorf("parallel map index %d: %w", i, err)
			}
			// 每个任务独占一个结果槽位；池等待所有任务退出后才发布整个结果切片。
			results[i] = result
			return nil
		}
	}
	if err := ParallelWithLimit(ctx, workers, tasks...); err != nil {
		return nil, err
	}
	return results, nil
}
