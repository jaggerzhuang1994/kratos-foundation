package utils

import (
	"context"
	"time"
)

// NewTickerJob 创建一个周期性执行的定时任务
// 按照指定的间隔周期性执行 job 函数
// 当 job 返回错误或 context 被取消时停止执行
// 返回 job 执行的错误或 context 的错误
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
