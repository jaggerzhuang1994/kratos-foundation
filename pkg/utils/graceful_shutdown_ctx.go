package utils

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
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
			fmt.Println("shutdown signal: ctx.Done()")
			return
		case sig := <-c:
			fmt.Println("shutdown signal: ", sig)
			return
		}
	}()
	return ctx, cancel
}
