package common

import (
	"context"
	"time"
)

// NewTickerJob 周期性执行 job，直到出错或 ctx 取消。
func NewTickerJob(ctx context.Context, interval time.Duration, job func(context.Context) error) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		err := job(ctx)
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
