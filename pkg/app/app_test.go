package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/types/known/durationpb"
	"net/url"
	"testing/synctest"
)

type newAppTestInfo struct{}

// newTestApp 为直接测试内部生命周期的快照补齐生产构造的必需依赖。
func newTestApp(t testing.TB, snapshot appSnapshot, policy *StopPolicy) *App {
	t.Helper()
	if snapshot.context == nil {
		snapshot.context = context.Background()
	}
	if snapshot.logger == nil {
		snapshot.logger = kratoslog.NewStdLogger(io.Discard)
	}
	return newApp(snapshot, policy)
}

func (newAppTestInfo) ID() string                  { return "test-id" }
func (newAppTestInfo) Name() string                { return "test-name" }
func (newAppTestInfo) Version() string             { return "test-version" }
func (newAppTestInfo) Metadata() map[string]string { return nil }

func newAppTestConfigAndPolicy(t testing.TB) (Config, *StopPolicy) {
	t.Helper()
	manager := testconfig.Empty(t)
	config, err := NewConfig(manager)
	if err != nil {
		t.Fatal(err)
	}
	policy, cleanup, err := NewStopPolicy(
		config,
		manager,
		kratoslog.NewStdLogger(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return config, policy
}

func registerNewAppTestInfo(t testing.TB, spec *Spec) {
	t.Helper()
	spec.RegisterAppInfo(newAppTestInfo{})
}

func registerNewAppTestLogger(t testing.TB, spec *Spec) {
	t.Helper()
	spec.RegisterLogger(kratoslog.NewStdLogger(io.Discard))
}

func TestNewAppRejectsMissingAppInfo(t *testing.T) {
	config, policy := newAppTestConfigAndPolicy(t)
	spec := NewSpec()
	registerNewAppTestLogger(t, spec)

	app, err := NewApp(context.Background(), spec, config, policy, nil)
	if app != nil || err == nil || !strings.Contains(err.Error(), "app info") {
		t.Fatalf("NewApp(missing app info) = (%v, %v), want explicit app info error", app, err)
	}
}

func TestNewAppRejectsMissingLogger(t *testing.T) {
	config, policy := newAppTestConfigAndPolicy(t)
	spec := NewSpec()
	registerNewAppTestInfo(t, spec)

	app, err := NewApp(context.Background(), spec, config, policy, nil)
	if app != nil || err == nil || !strings.Contains(err.Error(), "logger") {
		t.Fatalf("NewApp(missing logger) = (%v, %v), want explicit logger error", app, err)
	}
}

func TestNewAppConstructsApplicationFromSpec(t *testing.T) {
	config, policy := newAppTestConfigAndPolicy(t)
	spec := NewSpec()
	registerNewAppTestInfo(t, spec)
	registerNewAppTestLogger(t, spec)

	application, err := NewApp(context.Background(), spec, config, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if application.App == nil {
		t.Fatal("App does not own a Kratos application")
	}
	if application.ID() != "test-id" || application.Name() != "test-name" {
		t.Fatalf("application identity = %q/%q", application.ID(), application.Name())
	}
	assertSpecPanic(t, ErrSpecFrozen, func() { spec.AddMetadata(map[string]string{"late": "value"}) })
}

func TestNewAppRestoresKratosGlobalLoggerAfterConstruction(t *testing.T) {
	original := foundationlog.GetLogger()
	previous := kratoslog.NewStdLogger(io.Discard)
	registered := kratoslog.NewStdLogger(io.Discard)
	foundationlog.SetLogger(previous)
	t.Cleanup(func() { foundationlog.SetLogger(original) })

	config, policy := newAppTestConfigAndPolicy(t)
	spec := NewSpec()
	registerNewAppTestInfo(t, spec)
	spec.RegisterLogger(registered)
	if _, err := NewApp(context.Background(), spec, config, policy, nil); err != nil {
		t.Fatal(err)
	}
	if got := foundationlog.GetLogger(); got != previous {
		t.Fatalf("global logger after NewApp has type %T, want previous %T", got, previous)
	}
}

func TestOnceStopSharesOneResultAcrossCallers(t *testing.T) {
	want := errors.New("stop failed")
	calls := 0
	release := make(chan struct{})
	stop := onceStop(func() error {
		calls++
		<-release
		return want
	})

	results := make(chan error, 2)
	go func() { results <- stop() }()
	go func() { results <- stop() }()
	close(release)
	for range 2 {
		if err := <-results; !errors.Is(err, want) {
			t.Fatalf("stop error = %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("stop calls = %d, want 1", calls)
	}
}

// TestNewAppServiceRegistrationSwitch 验证显式关闭优先于非 nil Registrar，且不阻断运行时。
func TestNewAppServiceRegistrationSwitch(t *testing.T) {
	initializeRuntimeSignals(t)
	for _, disabled := range []bool{false, true} {
		name := "default"
		if disabled {
			name = "disabled"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				spec := newApplicationTestSpec(t)
				if disabled {
					spec.DisableServiceRegistration()
					spec.DisableServiceRegistration()
				}
				release := make(chan struct{})
				spec.RegisterRuntime(&applicationStopRequestRuntime{release: release})
				registrar := new(registrarCallFake)
				application, err := NewApp(context.Background(), spec, applicationTestConfig(), newStaticStopPolicy(time.Second), registrar)
				if err != nil {
					t.Fatal(err)
				}
				result := make(chan error, 1)
				go func() { result <- application.Run() }()
				synctest.Wait()
				if !spec.Ready() {
					t.Fatal("application did not become ready")
				}
				close(release)
				synctest.Wait()
				if err := <-result; err != nil {
					t.Fatal(err)
				}
				want := 1
				if disabled {
					want = 0
				}
				if registered, deregistered := registrar.calls(); registered != want || deregistered != want {
					t.Fatalf("registration calls = %d/%d, want %d/%d", registered, deregistered, want, want)
				}
			})
		})
	}
}

// TestNewAppRollsBackStartupFailures 验证底层 SDK 入口同样经过受监督的 Endpoint。
func TestNewAppRollsBackStartupFailures(t *testing.T) {
	for _, test := range []struct {
		name         string
		endpointFail bool
		parentCancel bool
		runSDK       bool
	}{
		{name: "foundation endpoint failure", endpointFail: true},
		{name: "SDK endpoint failure", endpointFail: true, runSDK: true},
		{name: "SDK before-start failure", runSDK: true},
		{name: "canceled parent", parentCancel: true, runSDK: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			type contextKey string
			const key contextKey = "startup"
			parent, cancel := context.WithCancel(context.WithValue(context.Background(), key, "retained"))
			defer cancel()
			if test.parentCancel {
				cancel()
			}
			cause := errors.New("startup failed")
			if test.parentCancel {
				cause = context.Canceled
			}
			abortErr := errors.New("abort failed")
			beforeStopErr := errors.New("before-stop failed")
			afterStopErr := errors.New("after-stop failed")
			var events []string
			checkContext := func(ctx context.Context) error {
				info, ok := kratos.FromContext(ctx)
				if ctx.Value(key) != "retained" || !ok || info.ID() != "test-id" {
					err := errors.New("startup cleanup lost parent values or app info")
					t.Error(err)
					return err
				}
				return nil
			}
			first := &startupRollbackTestRuntime{name: "first", events: &events}
			failed := &startupRollbackTestRuntime{name: "failed", events: &events, abortErr: abortErr}
			last := &startupRollbackTestRuntime{name: "last", events: &events}
			if test.endpointFail {
				failed.endpointErr = cause
			}
			for _, runtime := range []*startupRollbackTestRuntime{first, failed, last} {
				runtime.abortContext = func(ctx context.Context) error {
					if err := checkContext(ctx); err != nil {
						return err
					}
					if _, ok := ctx.Deadline(); !ok || ctx.Err() != nil {
						err := errors.New("startup cleanup needs a live shutdown deadline")
						t.Error(err)
						return err
					}
					return nil
				}
			}
			plain := new(testServer)
			spec := NewSpec()
			registerNewAppTestInfo(t, spec)
			registerNewAppTestLogger(t, spec)
			for _, runtime := range []Runtime{first, plain, failed, last} {
				spec.RegisterRuntime(runtime)
			}
			beforeStartCalls, beforeStopCalls, afterStopCalls := 0, 0, 0
			spec.BeforeStart(func(context.Context) error {
				beforeStartCalls++
				return cause
			})
			spec.BeforeStop(func(ctx context.Context) error {
				beforeStopCalls++
				events = append(events, "before-stop")
				return errors.Join(beforeStopErr, checkContext(ctx))
			})
			waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
			defer waitCancel()
			spec.AfterStop(func(ctx context.Context) error {
				afterStopCalls++
				events = append(events, "after-stop")
				for _, runtime := range []*startupRollbackTestRuntime{first, failed, last} {
					if runtime.resourceOpen || runtime.aborts != 1 {
						return fmt.Errorf("after-stop ran before %s resource rollback", runtime.name)
					}
				}
				if err := spec.WaitReady(waitCtx); !errors.Is(err, context.Canceled) {
					return fmt.Errorf("startup failure did not broadcast stopping: %w", err)
				}
				return errors.Join(afterStopErr, checkContext(ctx), ctx.Err())
			})
			application, err := NewApp(parent, spec, applicationTestConfig(), newStaticStopPolicy(time.Second), nil)
			if err != nil {
				t.Fatal(err)
			}
			if test.runSDK {
				err = application.App.Run()
			} else {
				err = application.Run()
			}
			for _, want := range []error{cause, abortErr, beforeStopErr, afterStopErr} {
				if !errors.Is(err, want) {
					t.Fatalf("Run=%v, missing %v", err, want)
				}
			}
			if want := []string{"before-stop", "abort:last", "abort:failed", "abort:first", "after-stop"}; !reflect.DeepEqual(events, want) {
				t.Fatalf("startup failure events=%v, want %v", events, want)
			}
			if beforeStopCalls != 1 || afterStopCalls != 1 || plain.starts != 0 || plain.stops != 0 {
				t.Fatalf("hook calls=%d/%d, unstarted runtime=%d/%d", beforeStopCalls, afterStopCalls, plain.starts, plain.stops)
			}
			if want := 1; test.endpointFail || test.parentCancel {
				if beforeStartCalls != 0 {
					t.Fatalf("BeforeStart calls=%d, want 0", beforeStartCalls)
				}
			} else if beforeStartCalls != want {
				t.Fatalf("BeforeStart calls=%d, want %d", beforeStartCalls, want)
			}
			for _, runtime := range []*startupRollbackTestRuntime{first, failed, last} {
				if runtime.starts != 0 || runtime.stops != 0 {
					t.Fatalf("unstarted %s runtime calls=%d/%d", runtime.name, runtime.starts, runtime.stops)
				}
			}
		})
	}
}

type startupRollbackTestRuntime struct {
	testServer
	name         string
	events       *[]string
	endpointErr  error
	abortErr     error
	abortContext func(context.Context) error
	release      <-chan struct{}
	resourceOpen bool
	aborts       int
}

func (r *startupRollbackTestRuntime) Endpoint() (*url.URL, error) {
	r.resourceOpen = true
	return &url.URL{Scheme: "http", Host: "127.0.0.1:8000"}, r.endpointErr
}

func (r *startupRollbackTestRuntime) Start(context.Context) error {
	r.starts++
	if r.release != nil {
		<-r.release
		return ErrStopRequested
	}
	return nil
}

func (r *startupRollbackTestRuntime) AbortStartup(ctx context.Context) error {
	r.aborts++
	r.resourceOpen = false
	*r.events = append(*r.events, "abort:"+r.name)
	if r.abortContext != nil {
		return errors.Join(r.abortErr, r.abortContext(ctx))
	}
	return r.abortErr
}

func TestNewAppSuccessfulStartupDoesNotAbortResources(t *testing.T) {
	initializeRuntimeSignals(t)
	synctest.Test(t, func(t *testing.T) {
		var events []string
		release := make(chan struct{})
		runtime := &startupRollbackTestRuntime{name: "running", events: &events, release: release}
		spec := NewSpec()
		registerNewAppTestInfo(t, spec)
		registerNewAppTestLogger(t, spec)
		spec.AddSignals(syscall.SIGUSR2)
		spec.RegisterRuntime(runtime)
		application, err := NewApp(context.Background(), spec, applicationTestConfig(), newStaticStopPolicy(time.Second), nil)
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- application.App.Run() }()
		synctest.Wait()
		if !spec.Ready() {
			t.Fatal("application did not become ready")
		}
		close(release)
		synctest.Wait()
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		if runtime.aborts != 0 || runtime.starts != 1 || runtime.stops != 1 {
			t.Fatalf("successful lifecycle calls: starts=%d stops=%d aborts=%d", runtime.starts, runtime.stops, runtime.aborts)
		}
	})
}

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

type appLogFunc func(kratoslog.Level, ...any) error

func (f appLogFunc) Log(level kratoslog.Level, fields ...any) error { return f(level, fields...) }

func captureAppEvents() (kratoslog.Logger, chan map[string]any) {
	events := make(chan map[string]any, 64)
	return appLogFunc(func(level kratoslog.Level, fields ...any) error {
		event := map[string]any{"level": level}
		for i := 0; i+1 < len(fields); i += 2 {
			if key, ok := fields[i].(string); ok {
				event[key] = fields[i+1]
			}
		}
		if event["event"] != nil {
			events <- event
		}
		return nil
	}), events
}

func TestApplicationLogsIdentityBeforeRunAndLifecycleOnce(t *testing.T) {
	logger, events := captureAppEvents()
	spec := NewSpec()
	info := applicationTestInfo()
	info.metadata = map[string]string{"env": "staging", "hostname": "host-a", "private": "secret-metadata"}
	spec.RegisterAppInfo(info)
	spec.RegisterLogger(logger)
	runtime := newApplicationRuntime()
	spec.RegisterRuntime(runtime)
	assembled := false
	spec.BeforeStart(func(context.Context) error {
		if !assembled {
			return errors.New("startup summary missing before start")
		}
		return nil
	})
	application, err := NewApp(context.Background(), spec, applicationTestConfig(), newStaticStopPolicy(time.Second), nil)
	if err != nil {
		t.Fatal(err)
	}
	first := <-events
	assembled = true
	if first["event"] != "app.assembled" || first["level"] != kratoslog.LevelInfo || first["module"] != "app" ||
		first["service.id"] != info.ID() || first["service.name"] != info.Name() || first["service.version"] != info.Version() ||
		first["env"] != "staging" || first["hostname"] != "host-a" || first["runtimes"] != 1 || first["service_registration"] != false ||
		first["go_version"] == "" || first["executable"] == "" || first["pid"] != os.Getpid() || first["msg"] != "application assembled" {
		t.Fatalf("startup summary = %v", first)
	}
	for key, value := range first {
		if key == "private" || value == "secret-metadata" {
			t.Fatalf("metadata leaked: %v", first)
		}
	}
	result := make(chan error, 1)
	go func() { result <- application.Run() }()
	receiveSignal(t, runtime.started, "runtime start")
	if err := spec.WaitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := application.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := receiveError(t, result, "application Run"); err != nil {
		t.Fatal(err)
	}
	if err := application.Stop(); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{"app.assembled": 1}
	for len(events) > 0 {
		event := <-events
		name := event["event"].(string)
		counts[name]++
		if event["level"] != kratoslog.LevelInfo || event["module"] != "app" || event["service.name"] != info.Name() {
			t.Fatalf("lifecycle event = %v", event)
		}
		if name == "app.stopped" && event["result"] != "success" {
			t.Fatalf("stop result = %v", event)
		}
	}
	for _, name := range []string{"app.assembled", "app.ready", "app.stopping", "app.stopped"} {
		if counts[name] != 1 {
			t.Fatalf("event counts = %v", counts)
		}
	}
}

func TestApplicationStartupFailureLogsTerminalError(t *testing.T) {
	logger, events := captureAppEvents()
	spec := NewSpec()
	spec.RegisterAppInfo(applicationTestInfo())
	spec.RegisterLogger(logger)
	cause := errors.New("before start failed")
	spec.BeforeStart(func(context.Context) error { return cause })
	application, err := NewApp(context.Background(), spec, applicationTestConfig(), newStaticStopPolicy(time.Second), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := application.Run(); !errors.Is(err, cause) {
		t.Fatalf("Run = %v", err)
	}
	stopped := 0
	for len(events) > 0 {
		event := <-events
		if event["event"] == "app.ready" {
			t.Fatal("failed startup logged ready")
		}
		if event["event"] == "app.stopped" {
			stopped++
			logged, ok := event["error"].(error)
			if event["level"] != kratoslog.LevelError || event["result"] != "failed" || !ok || !errors.Is(logged, cause) {
				t.Fatalf("failure event = %v", event)
			}
		}
	}
	if stopped != 1 {
		t.Fatalf("terminal events = %d", stopped)
	}
}

func TestNewAppRawLoggerHasOneApplicationModule(t *testing.T) {
	var output bytes.Buffer
	logger := kratoslog.NewStdLogger(&output)
	spec := NewSpec()
	spec.RegisterAppInfo(applicationTestInfo())
	spec.RegisterLogger(logger)
	if _, err := NewApp(context.Background(), spec, applicationTestConfig(), newStaticStopPolicy(time.Second), nil); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "module=") != 1 || !strings.Contains(output.String(), "module=app") || !strings.Contains(output.String(), "event=app.assembled") {
		t.Fatalf("startup event=%s", output.String())
	}
	output.Reset()
	if err := logger.Log(kratoslog.LevelInfo, "msg", "caller"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "module=") {
		t.Fatalf("input logger changed: %s", output.String())
	}
}
