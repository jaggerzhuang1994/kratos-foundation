package utils

import (
	"context"

	"golang.org/x/sync/errgroup"
)

func Parallel(fns ...func(ctx2 context.Context) (err error)) error {
	return ParallelWithContext(context.Background(), fns...)
}

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
