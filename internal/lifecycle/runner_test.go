package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRunnerStopCancelsRunAndWaits(t *testing.T) {
	runner := NewRunner()
	entered := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- runner.Run(context.Background(), func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			<-release
			return ctx.Err()
		})
	}()
	<-entered

	// 回调未返回前 Stop 受自身 Context 限制。
	expired, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := runner.Stop(expired); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop while running = %v, want deadline", err)
	}
	close(release)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want canceled", err)
	}
	// Run 已结束后即使 Context 已取消也返回 nil。
	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if err := runner.Stop(canceled); err != nil {
		t.Fatalf("Stop after completion = %v", err)
	}
}

func TestRunnerParentCancellationAndValues(t *testing.T) {
	type key struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "v"))
	runner := NewRunner()
	result := make(chan error, 1)
	go func() {
		result <- runner.Run(parent, func(ctx context.Context) error {
			if ctx.Value(key{}) != "v" {
				return errors.New("value lost")
			}
			<-ctx.Done()
			return nil
		})
	}()
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("Run = %v", err)
	}
}

func TestRunnerStopBeforeRunSkipsCallback(t *testing.T) {
	runner := NewRunner()
	if err := runner.Stop(context.Background()); err != nil {
		t.Fatalf("Stop before Run = %v", err)
	}
	called := false
	if err := runner.Run(context.Background(), func(context.Context) error { called = true; return nil }); err != nil || called {
		t.Fatalf("Run after Stop err=%v called=%v", err, called)
	}
}

func TestRunnerRejectsSecondRun(t *testing.T) {
	runner := NewRunner()
	want := errors.New("first")
	if err := runner.Run(context.Background(), func(context.Context) error { return want }); !errors.Is(err, want) {
		t.Fatalf("first Run = %v", err)
	}
	if err := runner.Run(context.Background(), func(context.Context) error { return nil }); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("second Run = %v", err)
	}
}

func TestRunnerConcurrentStartStop(t *testing.T) {
	for range 200 {
		runner := NewRunner()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = runner.Run(context.Background(), func(ctx context.Context) error {
				<-ctx.Done()
				return nil
			})
		}()
		go func() {
			defer wg.Done()
			if err := runner.Stop(context.Background()); err != nil {
				t.Errorf("Stop = %v", err)
			}
		}()
		wg.Wait()
	}
}
