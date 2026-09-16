package bootstrap_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/text"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
)

func TestUnifiedSpecSharesHooksAndFreeze(t *testing.T) {
	spec := newTestSpec()
	application := spec.application
	if spec.Http() != spec.servers.HTTP() || spec.Grpc() != spec.servers.GRPC() ||
		spec.Health() != spec.servers.Health() || spec.Job() != spec.jobs {
		t.Fatal("bootstrap did not retain the injected domain Specs")
	}
	if got := spec.AddContext(func(ctx context.Context) context.Context { return ctx }).
		AddMetadata(map[string]string{"blueprint": "ready"}).AddEndpoints().AddSignals().
		AfterStart().BeforeStop().AfterStop(); got != spec.Spec {
		t.Fatal("chain returned a different Spec")
	}
	started := false
	spec.BeforeStart(func(context.Context) error { started = true; return nil })
	spec.Job().RegisterOnce("finish", job.TaskFunc(func(context.Context) error { return nil })).ExitWhenDone()
	logger, tracing, metrics := newTestObservability(t)
	serverBootstrap := prepareServer(t, spec)
	_, err := bootstrap.NewJobBootstrap(spec.application, spec.jobs, nil, logger, metrics, tracing, serverBootstrap)
	if err != nil {
		t.Fatal(err)
	}
	if err := runComponentsApp(t, application); err != nil {
		t.Fatal(err)
	}
	if !started {
		t.Fatal("hook registered through bootstrap.Spec did not run")
	}
	assertBootstrapPanic(t, app.ErrSpecFrozen, func() { spec.BeforeStart() })
}

// 防止匿名嵌入重新将内部生命周期装配能力暴露给业务。
func TestSpecHidesInfrastructureAssembly(t *testing.T) {
	typ := reflect.TypeOf((*bootstrap.Spec)(nil))
	for _, name := range []string{"RegisterAppInfo", "RegisterLogger", "Runtime", "Ready", "App", "Log"} {
		if _, ok := typ.MethodByName(name); ok {
			t.Errorf("internal method %s is exposed", name)
		}
	}
	fields := typ.Elem()
	for i := 0; i < fields.NumField(); i++ {
		if fields.Field(i).IsExported() {
			t.Errorf("assembly field %s is exposed", fields.Field(i).Name)
		}
	}
}

func TestConfigurationOrdersSourcesBeforeProvidingManager(t *testing.T) {
	spec := newTestSpec()
	var calls []int
	loaders := []config.SourceLoader{}
	for i := 1; i <= 2; i++ {
		loaders = append(loaders, func() (config.Sources, error) {
			calls = append(calls, i)
			value := "first"
			if i == 2 {
				value = "second"
			}
			source, err := text.NewSource("test", config.JSONFormat, `{"phase":"`+value+`"}`)
			return config.Sources{source}, err
		})
	}
	if got := spec.Configuration(loaders...); got != spec.Spec {
		t.Fatal("Configuration did not return the same Spec")
	}
	if len(calls) != 0 {
		t.Fatal("declaration executed loaders")
	}
	manager, cleanup, err := bootstrap.NewConfigManager(spec.Spec)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var value string
	if err := manager.Load("phase", &value); err != nil || value != "second" {
		t.Fatalf("%q %v", value, err)
	}
	if !reflect.DeepEqual(calls, []int{1, 2}) {
		t.Fatal(calls)
	}
}

func TestConfigurationFailureAndDefaultEnvironment(t *testing.T) {
	spec := newTestSpec()
	assertBootstrapPanic(t, "bootstrap: config source loader is nil", func() { spec.Configuration(nil) })
	expected := errors.New("source construction failed")
	spec.Configuration(func() (config.Sources, error) { return nil, expected })
	if _, cleanup, err := bootstrap.NewConfigManager(spec.Spec); !errors.Is(err, expected) || cleanup != nil {
		t.Fatalf("%v", err)
	}

	t.Setenv("FOUNDATION_CONFIGURATION_DEFAULT", "yes")
	manager, cleanup, err := bootstrap.NewConfigManager(newTestSpec().Spec)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var value string
	if err := manager.Load("FOUNDATION_CONFIGURATION_DEFAULT", &value); err != nil || value != "yes" {
		t.Fatalf("%q %v", value, err)
	}
}

func assertBootstrapPanic(t *testing.T, expected any, call func()) {
	t.Helper()
	defer func() {
		if got := recover(); got != expected {
			t.Fatalf("panic = %v, want %v", got, expected)
		}
	}()
	call()
	t.Fatal("expected panic")
}

// 不经过 NewRuntimeBootstrap，也应能从同一 app.Spec 直接构造并运行已登记实例。
func TestRegisterRuntimeContributesImmediately(t *testing.T) {
	spec := newTestSpec()
	runtime := &immediateRuntime{started: make(chan struct{})}
	spec.RegisterRuntime(runtime)
	if err := runComponentsApp(t, spec.application); err != nil {
		t.Fatal(err)
	}
	assertBootstrapPanic(t, app.ErrSpecFrozen, func() { spec.RegisterRuntime(runtime) })
	select {
	case <-runtime.started:
	default:
		t.Fatal("runtime was not registered directly")
	}
}

type immediateRuntime struct{ started chan struct{} }

func (r *immediateRuntime) Start(context.Context) error {
	close(r.started)
	return app.ErrStopRequested
}
func (*immediateRuntime) Stop(context.Context) error { return nil }

func TestConfigurationLoadFailure(t *testing.T) {
	spec := newTestSpec()
	spec.Configuration(func() (config.Sources, error) {
		source, err := text.NewSource("invalid", config.JSONFormat, "{")
		return config.Sources{source}, err
	})
	_, cleanup, err := bootstrap.NewConfigManager(spec.Spec)
	if cleanup != nil {
		cleanup()
	}
	if err == nil {
		t.Fatal("invalid configuration was accepted")
	}
}

// testSpec 模拟 Wire：每种领域 Spec 只构造一次，所有依赖处共享同一指针。
type testSpec struct {
	*bootstrap.Spec
	application *app.Spec
	servers     *server.Spec
	jobs        *job.Spec
}

func newTestSpec() *testSpec {
	application, servers, jobs := app.NewSpec(), server.NewSpec(), job.NewSpec()
	return &testSpec{
		Spec:        bootstrap.NewSpec(application, servers, jobs),
		application: application, servers: servers, jobs: jobs,
	}
}
