package utils

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-kratos/kratos/v2/log"
)

func GracefulShutdownCtx(ctx context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
	if len(signals) == 0 {
		signals = []os.Signal{
			syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGTERM,
		}
	}

	ctx, cancel := context.WithCancel(ctx)

	c := make(chan os.Signal, 1)
	signal.Notify(c, signals...)

	go func() {
		defer cancel()
		select {
		case <-ctx.Done():
			log.Context(ctx).Info("shutdown signal: ctx.Done()")
			return
		case sig := <-c:
			log.Context(ctx).Info("shutdown signal: ", sig)
			return
		}
	}()
	return ctx, cancel
}
