package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

func TestApplicationRunPreservesRuntimeFailureJoinedWithCancellation(t *testing.T) {
	initializeRuntimeSignals(t)
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("runtime failed")
		release := make(chan struct{})
		spec := newApplicationTestSpec(t)
		if err := spec.AddSignals(syscall.SIGUSR2); err != nil {
			t.Fatal(err)
		}
		runtime := &cancellationFailureRuntime{release: release, startErr: errors.Join(context.Canceled, failure)}
		registerApplicationRuntime(t, spec, runtime)
		application, err := NewApp(context.Background(), spec, applicationTestConfig(), newStaticStopPolicy(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- application.Run() }()
		// 等待 Run 完成启动并阻塞在运行阶段，避免测试偶然走到 AfterStart 故障分支。
		synctest.Wait()
		close(release)
		synctest.Wait()
		if err := <-result; !errors.Is(err, failure) {
			t.Fatalf("Run = %v, want runtime failure", err)
		}
	})
}

func TestApplicationRunPreservesStopFailureJoinedWithCancellation(t *testing.T) {
	initializeRuntimeSignals(t)
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("runtime stop failed")
		release := make(chan struct{})
		spec := newApplicationTestSpec(t)
		if err := spec.AddSignals(syscall.SIGUSR2); err != nil {
			t.Fatal(err)
		}
		runtime := &cancellationFailureRuntime{release: release, startErr: ErrStopRequested, stopErr: errors.Join(context.Canceled, failure)}
		registerApplicationRuntime(t, spec, runtime)
		application, err := NewApp(context.Background(), spec, applicationTestConfig(), newStaticStopPolicy(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- application.Run() }()
		synctest.Wait()
		close(release)
		synctest.Wait()
		if err := <-result; !errors.Is(err, failure) {
			t.Fatalf("Run = %v, want stop failure", err)
		}
	})
}

type cancellationFailureRuntime struct {
	release  <-chan struct{}
	startErr error
	stopErr  error
}

func (r *cancellationFailureRuntime) Start(context.Context) error {
	<-r.release
	return r.startErr
}

func (r *cancellationFailureRuntime) Stop(context.Context) error { return r.stopErr }

// Kratos Run 会注册系统信号；信号线程须在 synctest bubble 外初始化。
func initializeRuntimeSignals(t *testing.T) {
	t.Helper()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGUSR2)
	t.Cleanup(func() {
		// Kratos 未清理 Run 内登记的信号 channel；测试只使用独立信号并在退出时移除。
		signal.Stop(signals)
		signal.Reset(syscall.SIGUSR2)
	})
}

func TestApplicationRunTreatsCancellationOnlyAsCleanShutdown(t *testing.T) {
	initializeRuntimeSignals(t)
	for _, test := range []struct {
		name              string
		startErr, stopErr error
	}{
		{name: "start cancellation", startErr: context.Canceled},
		{name: "wrapped start cancellation", startErr: fmt.Errorf("runtime: %w", context.Canceled)},
		{name: "joined start cancellation", startErr: errors.Join(context.Canceled, context.Canceled)},
		{name: "stop cancellation", startErr: ErrStopRequested, stopErr: context.Canceled},
		{name: "wrapped stop cancellation", startErr: ErrStopRequested, stopErr: fmt.Errorf("runtime: %w", context.Canceled)},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				release := make(chan struct{})
				spec := newApplicationTestSpec(t)
				if err := spec.AddSignals(syscall.SIGUSR2); err != nil {
					t.Fatal(err)
				}
				registerApplicationRuntime(t, spec, &cancellationFailureRuntime{release: release, startErr: test.startErr, stopErr: test.stopErr})
				application, err := NewApp(context.Background(), spec, applicationTestConfig(), newStaticStopPolicy(time.Second))
				if err != nil {
					t.Fatal(err)
				}
				result := make(chan error, 1)
				go func() { result <- application.Run() }()
				synctest.Wait()
				close(release)
				synctest.Wait()
				if err := <-result; err != nil {
					t.Fatalf("Run = %v, want clean shutdown", err)
				}
			})
		})
	}
}
