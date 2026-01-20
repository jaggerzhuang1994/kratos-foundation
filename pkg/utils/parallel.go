package utils

import (
	"context"

	"golang.org/x/sync/errgroup"
)

// Parallel 并发执行多个函数，使用默认的 background context
// 任何一个函数返回错误，则立即取消其他正在执行的函数
// 返回第一个遇到的错误或 nil
func Parallel(fns ...func(ctx2 context.Context) (err error)) error {
	return ParallelWithContext(context.Background(), fns...)
}

// ParallelWithContext 并发执行多个函数，使用指定的 context
// 任何一个函数返回错误或 context 被取消，则立即停止所有执行
// 返回第一个遇到的错误或 nil
func ParallelWithContext(ctx context.Context, fns ...func(ctx2 context.Context) (err error)) error {
	g, ctx2 := errgroup.WithContext(ctx)
	for i := range fns {
		fn := fns[i]
		g.Go(func() error {
			return fn(ctx2)
		})
	}
	return g.Wait()
}
