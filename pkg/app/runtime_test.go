package app

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/transport"
)

type testServer struct {
	startErr, stopErr error
	starts, stops     int
}

func (s *testServer) Start(context.Context) error { s.starts++; return s.startErr }
func (s *testServer) Stop(context.Context) error  { s.stops++; return s.stopErr }

// This kills mutations that lose runtime start failures, omit the application
// stop request, or invoke the wrapped Stop more than once.
func TestAppServerPropagatesFailureAndStopsOnce(t *testing.T) {
	failure := errors.New("start failed")
	server := &testServer{startErr: failure}
	stopCalls := 0
	stop := func() error { stopCalls++; return nil }
	runner := newApp(appSnapshot{}, newStaticStopPolicy(time.Second))
	runner.initServers(1)
	runner.stop = stop
	runtime := runner.wrapServer(server)
	err := runtime.Start(context.Background())
	if !errors.Is(err, failure) || stopCalls != 1 || !runner.isStopping() {
		t.Fatalf("Start = %v, stops=%d, stopping=%v", err, stopCalls, runner.isStopping())
	}
	// 最后一个完成信号会汇总启动故障；重复 Stop 必须复用同一错误结果。
	if err := runtime.Stop(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("Stop = %v, want start failure", err)
	}
	if err := runtime.Stop(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("repeated Stop = %v, want start failure", err)
	}
	if server.stops != 1 {
		t.Fatalf("wrapped Stop calls = %d", server.stops)
	}
}

func TestAppServerTreatsStopRequestAsCleanShutdown(t *testing.T) {
	server := &testServer{startErr: ErrStopRequested}
	stopCalls := 0
	runner := newApp(appSnapshot{}, newStaticStopPolicy(time.Second))
	runner.initServers(1)
	runner.stop = func() error { stopCalls++; return nil }
	runtime := runner.wrapServer(server)

	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	if stopCalls != 1 || !runner.isStopping() {
		t.Fatalf("stops=%d stopping=%v", stopCalls, runner.isStopping())
	}
}

func TestAppServerDoesNotSwallowJoinedStopRequestFailure(t *testing.T) {
	failure := errors.New("start failed")
	server := &testServer{startErr: errors.Join(ErrStopRequested, failure)}
	stopCalls := 0
	runner := newApp(appSnapshot{}, newStaticStopPolicy(time.Second))
	runner.initServers(1)
	runner.stop = func() error { stopCalls++; return nil }
	runtime := runner.wrapServer(server)

	err := runtime.Start(context.Background())
	if !errors.Is(err, ErrStopRequested) || !errors.Is(err, failure) {
		t.Fatalf("Start error = %v, want joined stop request and failure", err)
	}
	if got := runner.failureOr(nil); !errors.Is(got, failure) {
		t.Fatalf("recorded failure = %v, want %v", got, failure)
	}
	if stopCalls != 1 || !runner.isStopping() {
		t.Fatalf("stops=%d stopping=%v", stopCalls, runner.isStopping())
	}
}

type endpointTestServer struct {
	testServer
	endpoint    *url.URL
	endpointErr error
}

func (s *endpointTestServer) Endpoint() (*url.URL, error) {
	return s.endpoint, s.endpointErr
}

func TestAppCompletionWaitCompletesOrTimesOutAndRunsFinalHooks(t *testing.T) {
	empty := newApp(appSnapshot{}, newStaticStopPolicy(time.Second))
	empty.initServers(0)
	if err := empty.wait(time.Second); err != nil {
		t.Fatalf("empty tracker wait = %v", err)
	}

	afterStopErr := errors.New("after stop failed")
	afterCalls := 0
	lifecycle := newApp(appSnapshot{afterStop: []HookFunc{
		func(context.Context) error {
			afterCalls++
			return afterStopErr
		},
	}}, newStaticStopPolicy(time.Second))
	lifecycle.initServers(1)
	if err := lifecycle.wait(time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("incomplete tracker wait = %v, want deadline exceeded", err)
	}
	if err := lifecycle.complete(context.Background()); err != nil {
		t.Fatal(err)
	}
	lifecycle.requestStop()
	if err := lifecycle.complete(context.Background()); !errors.Is(err, afterStopErr) {
		t.Fatalf("last completion error = %v, want after-stop failure", err)
	}
	if err := lifecycle.complete(context.Background()); err != nil {
		t.Fatalf("extra completion error = %v", err)
	}
	if err := lifecycle.wait(time.Second); err != nil {
		t.Fatalf("completed tracker wait = %v", err)
	}
	if afterCalls != 1 {
		t.Fatalf("after-stop hook calls = %d, want 1", afterCalls)
	}
}

func TestAppServerPreservesEndpointContract(t *testing.T) {
	want := &url.URL{Scheme: "grpc", Host: "127.0.0.1:9000"}
	server := &endpointTestServer{endpoint: want}
	lifecycle := newApp(appSnapshot{}, newStaticStopPolicy(time.Second))
	lifecycle.initServers(1)
	lifecycle.stop = func() error { return nil }
	wrapped := lifecycle.wrapServer(server)
	endpointRuntime, ok := wrapped.(transport.Endpointer)
	if !ok {
		t.Fatalf("wrapped endpoint server type = %T", wrapped)
	}
	got, err := endpointRuntime.Endpoint()
	if err != nil || got != want {
		t.Fatalf("Endpoint = (%v, %v), want %v", got, err, want)
	}

	cause := errors.New("endpoint failed")
	server.endpointErr = cause
	if _, err := endpointRuntime.Endpoint(); !errors.Is(err, cause) {
		t.Fatalf("Endpoint error = %v, want %v", err, cause)
	}
}

func TestAppParentContextTranslatesParentCancelAndStopsIdempotently(t *testing.T) {
	t.Run("parent cancellation", func(t *testing.T) {
		parent, cancel := context.WithCancel(context.Background())
		stopErr := errors.New("stop failed")
		stopCalls := 0
		stop := func() error {
			stopCalls++
			return stopErr
		}
		runtime := &App{
			parent:     parent,
			stop:       stop,
			parentDone: make(chan struct{}),
		}
		cancel()
		if err := runtime.waitParent(context.Background()); !errors.Is(err, stopErr) {
			t.Fatalf("Start after parent cancel = %v", err)
		}
		if stopCalls != 1 {
			t.Fatalf("stop calls = %d, want 1", stopCalls)
		}
	})

	t.Run("runtime stop", func(t *testing.T) {
		runtime := &App{
			parent:     context.Background(),
			stop:       func() error { return nil },
			parentDone: make(chan struct{}),
		}
		if err := runtime.stopParent(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := runtime.stopParent(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := runtime.waitParent(context.Background()); err != nil {
			t.Fatalf("Start after Stop = %v", err)
		}
	})

	t.Run("Kratos context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		runtime := &App{
			parent:     context.Background(),
			stop:       func() error { return nil },
			parentDone: make(chan struct{}),
		}
		if err := runtime.waitParent(ctx); err != nil {
			t.Fatalf("Start after runtime context cancel = %v", err)
		}
	})
}

func TestStopAfterFailureAggregatesRootStopWaitAndCleanupFailures(t *testing.T) {
	cause := errors.New("after-start failed")
	stopErr := errors.New("application stop failed")
	afterStopErr := errors.New("cleanup failed")
	lifecycle := newApp(appSnapshot{
		afterStop: []HookFunc{func(context.Context) error { return afterStopErr }},
	}, newStaticStopPolicy(time.Second))
	lifecycle.initServers(0)

	lifecycle.stop = func() error { return stopErr }
	err := lifecycle.stopAfterFailure(context.Background(), cause)
	for _, want := range []error{cause, stopErr, afterStopErr} {
		if !errors.Is(err, want) {
			t.Fatalf("stopAfterFailure error = %v, missing %v", err, want)
		}
	}
	if !lifecycle.isStopping() {
		t.Fatal("stopAfterFailure did not freeze the stop policy")
	}
}

func TestStopAfterFailureReportsTrackerDeadline(t *testing.T) {
	cause := errors.New("after-start failed")
	lifecycle := newApp(appSnapshot{}, newStaticStopPolicy(time.Millisecond))
	lifecycle.initServers(1)
	lifecycle.stop = func() error { return nil }
	err := lifecycle.stopAfterFailure(context.Background(), cause)
	if !errors.Is(err, cause) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stopAfterFailure timeout error = %v", err)
	}
}

func TestStopBeforeStartPreservesFirstFailureAndCleanupErrors(t *testing.T) {
	root := errors.New("first failure")
	late := errors.New("application stopping")
	stopErr := errors.New("stop failed")
	cleanupErr := errors.New("cleanup failed")
	lifecycle := newApp(appSnapshot{
		afterStop: []HookFunc{func(context.Context) error { return cleanupErr }},
	}, newStaticStopPolicy(time.Second))
	lifecycle.recordFailure(root)

	lifecycle.stop = func() error { return stopErr }
	err := lifecycle.stopBeforeStart(context.Background(), errors.Join(errAppStopping, late))
	for _, want := range []error{root, stopErr, cleanupErr} {
		if !errors.Is(err, want) {
			t.Fatalf("stopBeforeRuntimeStart error = %v, missing %v", err, want)
		}
	}
	if errors.Is(err, late) {
		t.Fatalf("shutdown noise replaced the first failure: %v", err)
	}
}
