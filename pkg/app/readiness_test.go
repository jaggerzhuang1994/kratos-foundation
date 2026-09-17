package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReadinessRequiresAllHooksAndRejectsStop(t *testing.T) {
	spec := NewSpec()
	if spec.Ready() {
		t.Fatal("unconstructed app is ready")
	}
	runner := newApp(appSnapshot{}, newStaticStopPolicy(time.Second))
	spec.application.Store(runner)
	runner.afterStart = []HookFunc{func(context.Context) error {
		if spec.Ready() {
			t.Error("ready while startup hook is running")
		}
		return nil
	}}
	if err := runner.runAfterStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !spec.Ready() {
		t.Fatal("completed startup not ready")
	}
	runner.requestStop()
	if spec.Ready() {
		t.Fatal("stop request still ready")
	}
	// 停机后即使已经进入的启动钩子返回，也不能恢复 readiness。
	if err := runner.runAfterStart(context.Background()); !errors.Is(err, errAppStopping) {
		t.Fatalf("late startup=%v", err)
	}
	if spec.Ready() {
		t.Fatal("late startup restored readiness")
	}
}

func TestFailedStartupNeverBecomesReady(t *testing.T) {
	spec := NewSpec()
	cause := errors.New("initialization failed")
	runner := newApp(appSnapshot{afterStart: []HookFunc{func(context.Context) error { return cause }}}, newStaticStopPolicy(time.Second))
	spec.application.Store(runner)
	if err := runner.runAfterStart(context.Background()); !errors.Is(err, cause) {
		t.Fatalf("startup=%v", err)
	}
	if spec.Ready() {
		t.Fatal("failed startup ready")
	}
}

func TestWaitReadyBlocksUntilAfterStartCompletes(t *testing.T) {
	spec := NewSpec()
	entered := make(chan struct{})
	release := make(chan struct{})
	runner := newApp(appSnapshot{
		readySignal: spec.readySignal,
		afterStart: []HookFunc{func(context.Context) error {
			close(entered)
			<-release
			return nil
		}},
	}, newStaticStopPolicy(time.Second))
	spec.application.Store(runner)

	waited := make(chan error, 1)
	go func() { waited <- spec.WaitReady(context.Background()) }()
	started := make(chan error, 1)
	go func() { started <- runner.runAfterStart(context.Background()) }()
	<-entered
	select {
	case err := <-waited:
		t.Fatalf("WaitReady returned before hooks completed: %v", err)
	default:
	}
	close(release)
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	if err := <-waited; err != nil {
		t.Fatal(err)
	}
}

func TestWaitReadyStopsOnContextCancellation(t *testing.T) {
	spec := NewSpec()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := spec.WaitReady(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitReady = %v, want context canceled", err)
	}
}
