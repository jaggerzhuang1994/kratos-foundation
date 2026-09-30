package app

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

type testRuntime struct{}

func (*testRuntime) Start(context.Context) error { return nil }
func (*testRuntime) Stop(context.Context) error  { return nil }

type testAppInfo struct{}

func (testAppInfo) ID() string                  { return "test-id" }
func (testAppInfo) Name() string                { return "test-name" }
func (testAppInfo) Version() string             { return "test-version" }
func (testAppInfo) Metadata() map[string]string { return map[string]string{"source": "info"} }

func TestFreezeAppliesContextOutsideLockAndPreservesBase(t *testing.T) {
	type contextKey string
	const key contextKey = "base"

	spec := NewSpec()
	spec.AddContext(func(ctx context.Context) context.Context {
		assertSpecPanic(t, ErrSpecFrozen, func() { spec.AddMetadata(map[string]string{"late": "value"}) })
		return context.WithValue(ctx, contextKey("decorated"), "value")
	})

	snapshot, err := spec.freeze(context.WithValue(context.Background(), key, "preserved"))
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.context.Value(key); got != "preserved" {
		t.Fatalf("base context value = %v, want preserved", got)
	}
	if got := snapshot.context.Value(contextKey("decorated")); got != "value" {
		t.Fatalf("decorated context value = %v, want value", got)
	}
	if _, exists := snapshot.metadata["late"]; exists {
		t.Fatal("post-freeze metadata contribution was included")
	}
}

func TestFreezeReportsNilContextContributionIndex(t *testing.T) {
	spec := NewSpec()
	spec.AddContext(func(context.Context) context.Context { return nil })

	_, err := spec.freeze(context.Background())
	if err == nil || !strings.Contains(err.Error(), "context contribution 1 returned nil") {
		t.Fatalf("freeze error = %v, want contribution index", err)
	}
}

func TestFreezeChainsContextContributionsInRegistrationOrder(t *testing.T) {
	type contextKey string
	const key contextKey = "chain"

	spec := NewSpec()
	var calls []string
	spec.AddContext(func(ctx context.Context) context.Context {
		calls = append(calls, "first")
		return context.WithValue(ctx, key, "first")
	})
	spec.AddContext(func(ctx context.Context) context.Context {
		calls = append(calls, "second")
		if got := ctx.Value(key); got != "first" {
			t.Fatalf("second context input = %v, want first", got)
		}
		return context.WithValue(ctx, key, "second")
	})

	snapshot, err := spec.freeze(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "first,second" {
		t.Fatalf("decorator calls = %q, want first,second", got)
	}
	if got := snapshot.context.Value(key); got != "second" {
		t.Fatalf("final context value = %v, want second", got)
	}
}

func TestFreezeMergesAppInfoMetadataBeforeExplicitMetadata(t *testing.T) {
	info := testAppInfo{}
	spec := NewSpec()
	spec.RegisterAppInfo(info)
	assertSpecPanic(t, "app info is already registered", func() { spec.RegisterAppInfo(info) })
	spec.AddMetadata(map[string]string{"source": "explicit", "extra": "value"})

	snapshot, err := spec.freeze(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.metadata["source"]; got != "explicit" {
		t.Fatalf("source metadata = %q, want explicit", got)
	}
	if got := snapshot.metadata["extra"]; got != "value" {
		t.Fatalf("extra metadata = %q, want value", got)
	}
}

func TestFreezeSnapshotIsolatedFromRegistrationInputs(t *testing.T) {
	metadata := map[string]string{"key": "original"}
	firstEndpoint, err := url.Parse("http://127.0.0.1:8000")
	if err != nil {
		t.Fatal(err)
	}
	secondEndpoint, err := url.Parse("http://127.0.0.1:9000")
	if err != nil {
		t.Fatal(err)
	}
	endpoints := []*url.URL{firstEndpoint}
	signals := []os.Signal{syscall.SIGTERM}

	spec := NewSpec()
	spec.AddMetadata(metadata)
	spec.AddEndpoints(endpoints...)
	spec.AddSignals(signals...)

	metadata["key"] = "changed"
	endpoints[0] = secondEndpoint
	signals[0] = os.Interrupt

	snapshot, err := spec.freeze(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.metadata["key"]; got != "original" {
		t.Fatalf("metadata = %q, want original", got)
	}
	if len(snapshot.endpoints) != 1 || snapshot.endpoints[0] != firstEndpoint {
		t.Fatalf("endpoints = %#v, want first endpoint", snapshot.endpoints)
	}
	if len(snapshot.signals) != 1 || snapshot.signals[0] != syscall.SIGTERM {
		t.Fatalf("signals = %#v, want SIGTERM", snapshot.signals)
	}
}

func TestSpecRejectsContributionsAfterFreeze(t *testing.T) {
	spec := NewSpec()
	if _, err := spec.freeze(context.Background()); err != nil {
		t.Fatal(err)
	}

	endpoint, err := url.Parse("http://127.0.0.1:8000")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		call func()
	}{
		{name: "runtime", call: func() { spec.RegisterRuntime(&testRuntime{}) }},
		{name: "app info", call: func() { spec.RegisterAppInfo(testAppInfo{}) }},
		{name: "logger", call: func() { spec.RegisterLogger(kratoslog.NewStdLogger(nil)) }},
		{name: "context", call: func() {
			spec.AddContext(func(ctx context.Context) context.Context { return ctx })
		}},
		{name: "metadata", call: func() { spec.AddMetadata(map[string]string{"key": "value"}) }},
		{name: "endpoints", call: func() { spec.AddEndpoints(endpoint) }},
		{name: "service registration", call: spec.DisableServiceRegistration},
		{name: "signals", call: func() { spec.AddSignals() }},
		{name: "before start", call: func() { spec.BeforeStart(func(context.Context) error { return nil }) }},
		{name: "after start", call: func() { spec.AfterStart(func(context.Context) error { return nil }) }},
		{name: "before stop", call: func() { spec.BeforeStop(func(context.Context) error { return nil }) }},
		{name: "after stop", call: func() { spec.AfterStop(func(context.Context) error { return nil }) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertSpecPanic(t, ErrSpecFrozen, tt.call)
		})
	}
}

func TestAddContextRetainsRepeatedDecorator(t *testing.T) {
	type key struct{}
	spec := NewSpec()
	decorate := func(ctx context.Context) context.Context {
		value, _ := ctx.Value(key{}).(int)
		return context.WithValue(ctx, key{}, value+1)
	}
	for range 2 {
		spec.AddContext(decorate)
	}
	snapshot, err := spec.freeze(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.context.Value(key{}); got != 2 {
		t.Fatalf("decorated value = %v, want 2", got)
	}
}

func TestRegisterRuntimePreservesOrderAndRepeatedInstances(t *testing.T) {
	spec := NewSpec()
	first, second := &orderedTestRuntime{id: 1}, &orderedTestRuntime{id: 2}
	for _, runtime := range []Runtime{first, second, first} {
		spec.RegisterRuntime(runtime)
	}
	snapshot, err := spec.freeze(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.runtimes) != 3 || snapshot.runtimes[0] != first || snapshot.runtimes[1] != second || snapshot.runtimes[2] != first {
		t.Fatalf("runtimes = %v, want registration order including duplicate", snapshot.runtimes)
	}
}

type orderedTestRuntime struct {
	testRuntime
	id int
}

func TestRegisteredAppLoggerKeepsBorrowedInput(t *testing.T) {
	var output bytes.Buffer
	logger := kratoslog.NewStdLogger(&output)
	spec := NewSpec()
	spec.RegisterLogger(logger)
	assertSpecPanic(t, "app logger is already registered", func() { spec.RegisterLogger(logger) })
	snapshot, err := spec.freeze(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.logger != logger {
		t.Fatal("spec replaced borrowed logger")
	}
	if err := snapshot.logger.Log(kratoslog.LevelInfo, "msg", "business event"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "module=") {
		t.Fatalf("registration added a module: %s", output.String())
	}
}

func assertSpecPanic(t *testing.T, expected any, call func()) {
	t.Helper()
	defer func() {
		if got := recover(); got != expected {
			t.Fatalf("panic = %v, want %v", got, expected)
		}
	}()
	call()
	t.Fatal("expected panic")
}

func TestReadinessRequiresAllHooksAndRejectsStop(t *testing.T) {
	spec := NewSpec()
	if spec.Ready() {
		t.Fatal("unconstructed app is ready")
	}
	runner := newTestApp(t, appSnapshot{}, newStaticStopPolicy(time.Second))
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
	runner := newTestApp(t, appSnapshot{afterStart: []HookFunc{func(context.Context) error { return cause }}}, newStaticStopPolicy(time.Second))
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
	runner := newTestApp(t, appSnapshot{
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

func TestWaitReadyRejectsAppStopBeforeAndAfterReady(t *testing.T) {
	for _, ready := range []bool{false, true} {
		name := "before ready"
		if ready {
			name = "after ready"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				spec := NewSpec()
				snapshot, err := spec.freeze(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				runner := newTestApp(t, snapshot, newStaticStopPolicy(time.Second))
				if ready {
					if err := runner.runAfterStart(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				waited := make(chan error, 3)
				if !ready {
					for range cap(waited) {
						go func() { waited <- spec.WaitReady(ctx) }()
					}
					synctest.Wait()
				}
				runner.requestStop()
				runner.requestStop()
				if ready {
					go func() { waited <- spec.WaitReady(ctx) }()
				}
				synctest.Wait()
				count := cap(waited)
				if ready {
					count = 1
				}
				for range count {
					select {
					case err := <-waited:
						if !errors.Is(err, context.Canceled) {
							t.Fatalf("WaitReady after Stop = %v, want canceled", err)
						}
					default:
						t.Fatal("App stop did not release Ready waiter")
					}
				}
			})
		})
	}
}
