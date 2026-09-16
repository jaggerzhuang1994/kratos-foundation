package app

import (
	"context"
	"errors"
	"io"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/types/known/durationpb"
)

type applicationAppInfo struct {
	id       string
	name     string
	version  string
	metadata map[string]string
}

func (i applicationAppInfo) ID() string                  { return i.id }
func (i applicationAppInfo) Name() string                { return i.name }
func (i applicationAppInfo) Version() string             { return i.version }
func (i applicationAppInfo) Metadata() map[string]string { return i.metadata }

type applicationRuntime struct {
	mu sync.Mutex

	startErr error
	stopErr  error
	starts   int
	stops    int
	started  chan struct{}
	startOne sync.Once
	stopped  chan struct{}
	stopOne  sync.Once
}

type applicationStopRequestRuntime struct {
	release <-chan struct{}
}

func (r *applicationStopRequestRuntime) Start(context.Context) error {
	<-r.release
	return ErrStopRequested
}

func (*applicationStopRequestRuntime) Stop(context.Context) error { return nil }

func newApplicationRuntime() *applicationRuntime {
	return &applicationRuntime{
		started: make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

func (r *applicationRuntime) Start(ctx context.Context) error {
	r.mu.Lock()
	r.starts++
	startErr := r.startErr
	r.mu.Unlock()
	r.startOne.Do(func() { close(r.started) })
	if startErr != nil {
		return startErr
	}
	select {
	case <-r.stopped:
	case <-ctx.Done():
	}
	return nil
}

func (r *applicationRuntime) Stop(context.Context) error {
	r.mu.Lock()
	r.stops++
	stopErr := r.stopErr
	r.mu.Unlock()
	r.stopOne.Do(func() { close(r.stopped) })
	return stopErr
}

func (r *applicationRuntime) counts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts, r.stops
}

type applicationHookCalls struct {
	mu    sync.Mutex
	calls []string
}

func (c *applicationHookCalls) add(value string) {
	c.mu.Lock()
	c.calls = append(c.calls, value)
	c.mu.Unlock()
}

func (c *applicationHookCalls) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

func applicationTestConfig() *config_pb.App {
	return &config_pb.App{
		RegistrarTimeout: durationpb.New(100 * time.Millisecond),
		StopTimeout:      durationpb.New(time.Second),
	}
}

func applicationTestInfo() applicationAppInfo {
	return applicationAppInfo{
		id:      "app-id",
		name:    "app-name",
		version: "v1.2.3",
		metadata: map[string]string{
			"identity": "runtime",
			"runtime":  "true",
		},
	}
}

func newApplicationTestSpec(t testing.TB) *Spec {
	t.Helper()
	spec := NewSpec()
	spec.RegisterAppInfo(applicationTestInfo())
	spec.RegisterLogger(kratoslog.NewStdLogger(io.Discard))
	return spec
}

func registerApplicationRuntime(t testing.TB, spec *Spec, runtime Runtime) {
	t.Helper()
	spec.RegisterRuntime(runtime)
}

func TestNewApplicationRunsSuccessfulLifecycleWithRegistration(t *testing.T) {
	type contextKey string
	const assembledKey contextKey = "assembled"
	calls := new(applicationHookCalls)
	afterStarted := make(chan struct{})
	spec := newApplicationTestSpec(t)
	spec.AddContext(func(ctx context.Context) context.Context {
		return context.WithValue(ctx, assembledKey, "yes")
	})
	spec.BeforeStart(func(ctx context.Context) error {
		if got := ctx.Value(assembledKey); got != "yes" {
			t.Fatalf("assembled context value = %v, want yes", got)
		}
		calls.add("before-start")
		return nil
	})
	spec.AfterStart(func(context.Context) error {
		calls.add("after-start")
		close(afterStarted)
		return nil
	})
	spec.BeforeStop(func(context.Context) error { calls.add("before-stop"); return nil })
	spec.AfterStop(func(context.Context) error { calls.add("after-stop"); return nil })
	registrar := new(registrarCallFake)
	spec.AddEndpoints(&url.URL{Scheme: "http", Host: "127.0.0.1:8000"})

	runtime := newApplicationRuntime()
	registerApplicationRuntime(t, spec, runtime)
	config := applicationTestConfig()
	config.Metadata = map[string]string{
		"identity": "static",
		"static":   "true",
	}
	config.Endpoints = []*config_pb.Endpoint{{
		Scheme: "grpc",
		Host:   "127.0.0.1:9000",
	}}
	app, err := NewApp(
		context.Background(),
		spec,
		config,
		newStaticStopPolicy(time.Second),
		registrar,
	)
	if err != nil {
		t.Fatal(err)
	}
	if app.ID() != "app-id" || app.Name() != "app-name" || app.Version() != "v1.2.3" {
		t.Fatalf("application identity = %q/%q/%q", app.ID(), app.Name(), app.Version())
	}
	if got := app.Metadata(); !reflect.DeepEqual(got, map[string]string{
		"identity": "runtime",
		"runtime":  "true",
		"static":   "true",
	}) {
		t.Fatalf("application metadata = %#v", got)
	}

	runResult := make(chan error, 1)
	go func() { runResult <- app.Run() }()
	receiveSignal(t, runtime.started, "application runtime start")
	receiveSignal(t, afterStarted, "after-start hook")
	if got := app.Endpoint(); !reflect.DeepEqual(got, []string{
		"grpc://127.0.0.1:9000",
		"http://127.0.0.1:8000",
	}) {
		t.Fatalf("application endpoints = %#v", got)
	}
	registerCalls, deregisterCalls := registrar.calls()
	if registerCalls != 1 || deregisterCalls != 0 {
		t.Fatalf("registry calls before stop = register %d, deregister %d", registerCalls, deregisterCalls)
	}

	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := receiveError(t, runResult, "successful application Run"); err != nil {
		t.Fatalf("application Run = %v", err)
	}
	starts, stops := runtime.counts()
	if starts != 1 || stops != 1 {
		t.Fatalf("runtime calls = start %d, stop %d", starts, stops)
	}
	registerCalls, deregisterCalls = registrar.calls()
	if registerCalls != 1 || deregisterCalls != 1 {
		t.Fatalf("registry calls after stop = register %d, deregister %d", registerCalls, deregisterCalls)
	}
	if got := calls.snapshot(); !reflect.DeepEqual(got, []string{
		"before-start",
		"after-start",
		"before-stop",
		"after-stop",
	}) {
		t.Fatalf("lifecycle hooks = %#v", got)
	}
}

func TestApplicationBeforeStartFailureStopsWithoutStartingRuntimes(t *testing.T) {
	cause := errors.New("before-start failed")
	afterStopped := make(chan struct{})
	spec := newApplicationTestSpec(t)
	spec.BeforeStart(func(context.Context) error { return cause })
	spec.AfterStop(func(context.Context) error { close(afterStopped); return nil })
	runtime := newApplicationRuntime()
	registerApplicationRuntime(t, spec, runtime)
	app, err := NewApp(
		context.Background(),
		spec,
		applicationTestConfig(),
		newStaticStopPolicy(time.Second),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Run(); !errors.Is(err, cause) {
		t.Fatalf("Run before-start failure = %v, want %v", err, cause)
	}
	receiveSignal(t, afterStopped, "after-stop hook")
	starts, stops := runtime.counts()
	if starts != 0 || stops != 0 {
		t.Fatalf("runtime started after precondition failure: start %d, stop %d", starts, stops)
	}
}

func TestApplicationAfterStartFailureStopsStartedRuntime(t *testing.T) {
	cause := errors.New("after-start failed")
	spec := newApplicationTestSpec(t)
	spec.AfterStart(func(context.Context) error { return cause })
	runtime := newApplicationRuntime()
	registerApplicationRuntime(t, spec, runtime)
	app, err := NewApp(
		context.Background(),
		spec,
		applicationTestConfig(),
		newStaticStopPolicy(time.Second),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Run(); !errors.Is(err, cause) {
		t.Fatalf("Run after-start failure = %v, want %v", err, cause)
	}
	starts, stops := runtime.counts()
	if starts != 1 || stops != 1 {
		t.Fatalf("runtime calls after startup failure = start %d, stop %d", starts, stops)
	}
}

func TestApplicationRuntimeFailureDuringAfterStartPreservesRootFailure(t *testing.T) {
	cause := errors.New("runtime failed")
	hookEntered := make(chan struct{})
	releaseRuntime := make(chan struct{})
	spec := newApplicationTestSpec(t)
	spec.AfterStart(func(ctx context.Context) error {
		close(hookEntered)
		<-ctx.Done()
		return ctx.Err()
	})
	registerApplicationRuntime(t, spec, &applicationFailureRuntime{
		hookEntered: hookEntered,
		release:     releaseRuntime,
		err:         cause,
	})

	app, err := NewApp(
		context.Background(),
		spec,
		applicationTestConfig(),
		newStaticStopPolicy(time.Second),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	runResult := make(chan error, 1)
	go func() { runResult <- app.Run() }()
	receiveSignal(t, hookEntered, "after-start hook")
	close(releaseRuntime)
	if err := receiveError(t, runResult, "application Run"); !errors.Is(err, cause) {
		t.Fatalf("application Run error = %v, want %v", err, cause)
	}
}

type applicationFailureRuntime struct {
	hookEntered <-chan struct{}
	release     <-chan struct{}
	err         error
}

func (r *applicationFailureRuntime) Start(context.Context) error {
	<-r.hookEntered
	<-r.release
	return r.err
}

func (*applicationFailureRuntime) Stop(context.Context) error { return nil }

func TestApplicationStopRequestIgnoresRegistrarCallbackCancellation(t *testing.T) {
	registerEntered := make(chan struct{})
	releaseRuntime := make(chan struct{})
	spec := newApplicationTestSpec(t)
	registrar := &registrarCallFake{registerFn: func(
		ctx context.Context,
		_ *registry.ServiceInstance,
	) error {
		close(registerEntered)
		<-ctx.Done()
		return ctx.Err()
	}}
	registerApplicationRuntime(t, spec, &applicationStopRequestRuntime{release: releaseRuntime})
	app, err := NewApp(
		context.Background(),
		spec,
		applicationTestConfig(),
		newStaticStopPolicy(time.Second),
		registrar,
	)
	if err != nil {
		t.Fatal(err)
	}

	runResult := make(chan error, 1)
	go func() { runResult <- app.Run() }()
	receiveSignal(t, registerEntered, "registrar callback entry")
	close(releaseRuntime)
	if err := receiveError(t, runResult, "application Run"); err != nil {
		t.Fatalf("application Run returned shutdown callback cancellation: %v", err)
	}
}

func TestApplicationStopRequestPreservesUnrelatedRegistrarCallbackError(t *testing.T) {
	want := errors.New("register callback failed")
	registerEntered := make(chan struct{})
	releaseRuntime := make(chan struct{})
	spec := newApplicationTestSpec(t)
	registrar := &registrarCallFake{registerFn: func(
		ctx context.Context,
		_ *registry.ServiceInstance,
	) error {
		close(registerEntered)
		<-ctx.Done()
		return want
	}}
	registerApplicationRuntime(t, spec, &applicationStopRequestRuntime{release: releaseRuntime})
	app, err := NewApp(
		context.Background(),
		spec,
		applicationTestConfig(),
		newStaticStopPolicy(time.Second),
		registrar,
	)
	if err != nil {
		t.Fatal(err)
	}

	runResult := make(chan error, 1)
	go func() { runResult <- app.Run() }()
	receiveSignal(t, registerEntered, "registrar callback entry")
	close(releaseRuntime)
	if err := receiveError(t, runResult, "application Run"); !errors.Is(err, want) {
		t.Fatalf("application Run error = %v, want %v", err, want)
	}
}

func TestApplicationStopRequestIgnoresAfterStartCallbackCancellation(t *testing.T) {
	hookEntered := make(chan struct{})
	releaseRuntime := make(chan struct{})
	spec := newApplicationTestSpec(t)
	spec.AfterStart(func(ctx context.Context) error {
		close(hookEntered)
		<-ctx.Done()
		return ctx.Err()
	})
	registerApplicationRuntime(t, spec, &applicationStopRequestRuntime{release: releaseRuntime})
	app, err := NewApp(
		context.Background(),
		spec,
		applicationTestConfig(),
		newStaticStopPolicy(time.Second),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	runResult := make(chan error, 1)
	go func() { runResult <- app.Run() }()
	receiveSignal(t, hookEntered, "after-start callback entry")
	close(releaseRuntime)
	if err := receiveError(t, runResult, "application Run"); err != nil {
		t.Fatalf("application Run returned shutdown callback cancellation: %v", err)
	}
}

func TestApplicationStopRequestPreservesUnrelatedAfterStartCallbackError(t *testing.T) {
	want := errors.New("after-start callback failed")
	hookEntered := make(chan struct{})
	releaseRuntime := make(chan struct{})
	spec := newApplicationTestSpec(t)
	spec.AfterStart(func(ctx context.Context) error {
		close(hookEntered)
		<-ctx.Done()
		return want
	})
	registerApplicationRuntime(t, spec, &applicationStopRequestRuntime{release: releaseRuntime})
	app, err := NewApp(
		context.Background(),
		spec,
		applicationTestConfig(),
		newStaticStopPolicy(time.Second),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	runResult := make(chan error, 1)
	go func() { runResult <- app.Run() }()
	receiveSignal(t, hookEntered, "after-start callback entry")
	close(releaseRuntime)
	if err := receiveError(t, runResult, "application Run"); !errors.Is(err, want) {
		t.Fatalf("application Run error = %v, want %v", err, want)
	}
}

func TestApplicationCanceledParentStopsBeforeRuntimeStart(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	spec := newApplicationTestSpec(t)
	runtime := newApplicationRuntime()
	registerApplicationRuntime(t, spec, runtime)
	app, err := NewApp(
		parent,
		spec,
		applicationTestConfig(),
		newStaticStopPolicy(time.Second),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Run(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run with canceled parent = %v, want context.Canceled", err)
	}
	starts, stops := runtime.counts()
	if starts != 0 || stops != 0 {
		t.Fatalf("runtime calls = start %d, stop %d; want no runtime start", starts, stops)
	}
}
