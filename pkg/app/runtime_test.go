package app

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

type testServer struct {
	startErr, stopErr error
	starts, stops     int
}

func (s *testServer) Start(context.Context) error { s.starts++; return s.startErr }
func (s *testServer) Stop(context.Context) error  { s.stops++; return s.stopErr }

// runLifecycle 运行一个只登记给定 Runtime 的应用，返回 Run 结果；release 关闭后 Runtime 才从 Start 返回。
func runLifecycle(t *testing.T, runtime Runtime, configure func(*Spec)) error {
	t.Helper()
	spec := newApplicationTestSpec(t)
	spec.AddSignals(syscall.SIGUSR2)
	if configure != nil {
		configure(spec)
	}
	spec.RegisterRuntime(runtime)
	application, err := NewApp(context.Background(), spec, applicationTestConfig(), newStaticStopPolicy(time.Second), nil)
	if err != nil {
		t.Fatal(err)
	}
	return application.Run()
}

func TestRunPropagatesRuntimeFailureAndStopsEachRuntimeOnce(t *testing.T) {
	initializeRuntimeSignals(t)
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("start failed")
		release := make(chan struct{})
		failing := &cancellationFailureRuntime{release: release, startErr: failure}
		healthy := newApplicationRuntime()
		result := make(chan error, 1)
		go func() {
			result <- runLifecycle(t, failing, func(spec *Spec) { spec.RegisterRuntime(healthy) })
		}()
		synctest.Wait()
		close(release)
		if err := <-result; !errors.Is(err, failure) {
			t.Fatalf("Run = %v, want runtime failure", err)
		}
		if starts, stops := healthy.counts(); starts != 1 || stops != 1 {
			t.Fatalf("healthy runtime calls = %d/%d", starts, stops)
		}
	})
}

func TestRunTreatsStopRequestAsCleanShutdown(t *testing.T) {
	initializeRuntimeSignals(t)
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		result := make(chan error, 1)
		go func() {
			result <- runLifecycle(t, &cancellationFailureRuntime{release: release, startErr: ErrStopRequested}, nil)
		}()
		synctest.Wait()
		close(release)
		if err := <-result; err != nil {
			t.Fatalf("Run = %v, want clean shutdown", err)
		}
	})
}

func TestRunDoesNotSwallowJoinedStopRequestFailure(t *testing.T) {
	initializeRuntimeSignals(t)
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("start failed")
		release := make(chan struct{})
		result := make(chan error, 1)
		go func() {
			result <- runLifecycle(t, &cancellationFailureRuntime{release: release, startErr: errors.Join(ErrStopRequested, failure)}, nil)
		}()
		synctest.Wait()
		close(release)
		if err := <-result; !errors.Is(err, failure) {
			t.Fatalf("Run = %v, want joined failure", err)
		}
	})
}

type endpointTestServer struct {
	testServer
	endpoint    *url.URL
	endpointErr error
}

func (s *endpointTestServer) Endpoint() (*url.URL, error) {
	return s.endpoint, s.endpointErr
}

// 无显式端点时按登记顺序解析 Endpointer，Endpoint 在 Run 解析前为 nil。
func TestRunResolvesRuntimeEndpoints(t *testing.T) {
	initializeRuntimeSignals(t)
	synctest.Test(t, func(t *testing.T) {
		spec := newApplicationTestSpec(t)
		spec.AddSignals(syscall.SIGUSR2)
		spec.RegisterRuntime(&endpointTestServer{endpoint: &url.URL{Scheme: "grpc", Host: "127.0.0.1:9000"}})
		spec.RegisterRuntime(new(testServer))
		spec.RegisterRuntime(&endpointTestServer{endpoint: &url.URL{Scheme: "http", Host: "127.0.0.1:8000"}})
		application, err := NewApp(context.Background(), spec, applicationTestConfig(), newStaticStopPolicy(time.Second), nil)
		if err != nil {
			t.Fatal(err)
		}
		if application.Endpoint() != nil {
			t.Fatal("endpoints resolved before Run")
		}
		result := make(chan error, 1)
		go func() { result <- application.Run() }()
		synctest.Wait()
		if got := application.Endpoint(); !reflect.DeepEqual(got, []string{"grpc://127.0.0.1:9000", "http://127.0.0.1:8000"}) {
			t.Fatalf("Endpoint = %v", got)
		}
		if err := application.Stop(); err != nil {
			t.Fatal(err)
		}
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	})
}

// 父 Context 在运行期间取消时执行完整停机并正常返回。
func TestRunTranslatesParentCancellationIntoCleanStop(t *testing.T) {
	initializeRuntimeSignals(t)
	synctest.Test(t, func(t *testing.T) {
		parent, cancel := context.WithCancel(context.Background())
		spec := newApplicationTestSpec(t)
		spec.AddSignals(syscall.SIGUSR2)
		beforeStop := 0
		spec.BeforeStop(func(context.Context) error { beforeStop++; return nil })
		runtime := newApplicationRuntime()
		spec.RegisterRuntime(runtime)
		application, err := NewApp(parent, spec, applicationTestConfig(), newStaticStopPolicy(time.Second), nil)
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- application.Run() }()
		synctest.Wait()
		cancel()
		if err := <-result; err != nil {
			t.Fatalf("Run = %v", err)
		}
		if starts, stops := runtime.counts(); starts != 1 || stops != 1 || beforeStop != 1 {
			t.Fatalf("runtime %d/%d before-stop %d", starts, stops, beforeStop)
		}
	})
}

// 系统信号在运行期间触发统一停机。
func TestRunStopsOnSignal(t *testing.T) {
	initializeRuntimeSignals(t)
	runtime := newApplicationRuntime()
	result := make(chan error, 1)
	go func() { result <- runLifecycle(t, runtime, nil) }()
	receiveSignal(t, runtime.started, "runtime start")
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGUSR2); err != nil {
		t.Fatal(err)
	}
	if err := receiveError(t, result, "signal stop"); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if starts, stops := runtime.counts(); starts != 1 || stops != 1 {
		t.Fatalf("runtime calls = %d/%d", starts, stops)
	}
}

// 启动阶段失败时等待运行时退出受冻结预算约束，并保留根因与清理错误。
func TestRunStartupFailureBoundsRuntimeWait(t *testing.T) {
	initializeRuntimeSignals(t)
	synctest.Test(t, func(t *testing.T) {
		cause := errors.New("after-start failed")
		afterStopErr := errors.New("cleanup failed")
		stuck := make(chan struct{})
		defer close(stuck)
		spec := newApplicationTestSpec(t)
		spec.AfterStart(func(context.Context) error { return cause })
		spec.AfterStop(func(context.Context) error { return afterStopErr })
		// Start 不响应 Stop，模拟忽略取消的 Runtime。
		spec.RegisterRuntime(&blockingRuntime{release: stuck})
		application, err := NewApp(context.Background(), spec, applicationTestConfig(), newStaticStopPolicy(time.Second), nil)
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		err = application.Run()
		for _, want := range []error{cause, context.DeadlineExceeded, afterStopErr} {
			if !errors.Is(err, want) {
				t.Fatalf("Run = %v, missing %v", err, want)
			}
		}
		if elapsed := time.Since(started); elapsed != time.Second {
			t.Fatalf("bounded wait took %s, want frozen stop timeout", elapsed)
		}
	})
}

type blockingRuntime struct{ release <-chan struct{} }

func (r *blockingRuntime) Start(context.Context) error { <-r.release; return nil }
func (*blockingRuntime) Stop(context.Context) error    { return nil }

func TestAbortStartupPreservesFirstFailureAndCleanupErrors(t *testing.T) {
	root := errors.New("first failure")
	late := errors.New("application stopping")
	beforeStopErr := errors.New("before stop failed")
	cleanupErr := errors.New("cleanup failed")
	lifecycle := newTestApp(t, appSnapshot{
		beforeStop: []HookFunc{func(context.Context) error { return beforeStopErr }},
		afterStop:  []HookFunc{func(context.Context) error { return cleanupErr }},
	}, newStaticStopPolicy(time.Second))
	lifecycle.recordFailure(root)
	err := lifecycle.abortStartup(errors.Join(errAppStopping, late))
	for _, want := range []error{root, beforeStopErr, cleanupErr} {
		if !errors.Is(err, want) {
			t.Fatalf("abortStartup error = %v, missing %v", err, want)
		}
	}
	if errors.Is(err, late) {
		t.Fatalf("shutdown noise replaced the first failure: %v", err)
	}
}

func TestAbortStartupUsesOneUncanceledBudgetAndContinuesAfterTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		type contextKey string
		const key contextKey = "startup"
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "retained"))
		cancel()
		var events []string
		first := &startupRollbackTestRuntime{name: "first", events: &events, abortContext: func(ctx context.Context) error {
			if ctx.Value(key) != "retained" || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.Errorf("later cleanup context=%v value=%v", ctx.Err(), ctx.Value(key))
			}
			return ctx.Err()
		}}
		last := &startupRollbackTestRuntime{name: "last", events: &events, abortContext: func(ctx context.Context) error {
			if ctx.Value(key) != "retained" || ctx.Err() != nil {
				t.Errorf("first cleanup context=%v value=%v", ctx.Err(), ctx.Value(key))
			}
			<-ctx.Done()
			return ctx.Err()
		}}
		lifecycle := newTestApp(t, appSnapshot{context: ctx, runtimes: []Runtime{first, last}}, newStaticStopPolicy(time.Second))
		if err := lifecycle.abortStartup(nil); !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			t.Fatalf("abortStartup error=%v, want only deadline exceeded", err)
		}
		if want := []string{"abort:last", "abort:first"}; !reflect.DeepEqual(events, want) {
			t.Fatalf("abortStartup events=%v, want %v", events, want)
		}
	})
}
