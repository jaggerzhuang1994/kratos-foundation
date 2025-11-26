package utils

import (
	"context"
	"time"
)

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
