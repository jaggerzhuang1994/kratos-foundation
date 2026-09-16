package job

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCronJobPreservesBusinessErrorsDuringCancellation(t *testing.T) {
	failure := errors.New("persist job result failed")
	tests := []struct {
		name         string
		err          error
		wantReported bool
	}{
		{name: "normal exit"},
		{name: "cancellation", err: context.Canceled},
		{name: "wrapped cancellation", err: fmt.Errorf("stop: %w", context.Canceled)},
		{name: "joined cancellations", err: errors.Join(context.Canceled, fmt.Errorf("stop: %w", context.Canceled))},
		{name: "business failure", err: failure, wantReported: true},
		{name: "joined business failure", err: errors.Join(context.Canceled, failure), wantReported: true},
		{name: "nested business failure", err: fmt.Errorf("cleanup: %w", errors.Join(context.Canceled, failure)), wantReported: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var reported error
			job := cronJob{
				ctx:          ctx,
				name:         "persist",
				job:          TaskFunc(func(context.Context) error { return tt.err }),
				errorHandler: func(_ context.Context, _ string, err error) { reported = err },
			}
			job.Run()
			if tt.wantReported {
				if !errors.Is(reported, failure) {
					t.Fatalf("reported error = %v, want business failure", reported)
				}
			} else if reported != nil {
				t.Fatalf("reported error = %v, want normal shutdown", reported)
			}
		})
	}
}

func TestManagedJobRecoversEntireExecutionChain(t *testing.T) {
	for _, phase := range []string{"before next", "after next", "defer"} {
		t.Run(phase, func(t *testing.T) {
			panicMiddleware := func(next Handler) Handler {
				return func(ctx context.Context) error {
					if phase == "before next" {
						panic(phase)
					}
					if phase == "defer" {
						defer func() { panic(phase) }()
					}
					err := next(ctx)
					if phase == "after next" {
						panic(phase)
					}
					return err
				}
			}
			// 外围中间件和它的 defer 均由同一个最外层 recovery 捕获。
			gate := newExecutionGate(testModuleLog(t), "test", cronConfig{policy: DelayIfRunning, pending: 0}, nil)
			task := newManagedJob("test", TaskFunc(func(context.Context) error { return nil }), []Middleware{gate.middleware, panicMiddleware})
			var reported error
			trigger := cronJob{ctx: context.Background(), name: "test", job: task.job, errorHandler: func(_ context.Context, _ string, err error) { reported = err }}
			trigger.Run()
			if gate.running != 0 {
				t.Fatal("panic leaked execution slot")
			}
			if reported == nil || !strings.Contains(reported.Error(), "job panic: "+phase) {
				t.Fatalf("panic escaped final error path: %v", reported)
			}
		})
	}
}
