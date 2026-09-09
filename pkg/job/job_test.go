package job

import (
	"context"
	"errors"
	"fmt"
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
