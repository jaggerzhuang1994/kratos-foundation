package utils

import (
	"context"
	"time"
)

// SleepWithContext 支持上下文取消的睡眠函数
// 可以在睡眠期间响应上下文的取消或超时
// 返回上下文的错误（如果被取消）或 nil
func SleepWithContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err() // deadline exceeded / canceled
	case <-t.C:
		return nil
	}
}
