package app

import (
	"context"
	"errors"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-kratos/kratos/v2/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	foundationregistry "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

type registrarCallFake struct {
	mu sync.Mutex

	registerCalls   int
	deregisterCalls int
	registered      *registry.ServiceInstance
	deregistered    *registry.ServiceInstance
	registerFn      func(context.Context, *registry.ServiceInstance) error
	deregisterFn    func(context.Context, *registry.ServiceInstance) error
}

func (f *registrarCallFake) Register(
	ctx context.Context,
	instance *registry.ServiceInstance,
) error {
	f.mu.Lock()
	f.registerCalls++
	f.registered = instance
	fn := f.registerFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, instance)
	}
	return nil
}

func (f *registrarCallFake) Deregister(
	ctx context.Context,
	instance *registry.ServiceInstance,
) error {
	f.mu.Lock()
	f.deregisterCalls++
	f.deregistered = instance
	fn := f.deregisterFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, instance)
	}
	return nil
}

func (f *registrarCallFake) calls() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registerCalls, f.deregisterCalls
}

// newRegistrarTestApp 构造只验证注册协调的 App；Run 中的注册前置步骤由 NewApp 黑盒测试覆盖。
func newRegistrarTestApp(t testing.TB, registrar registry.Registrar, snapshot appSnapshot) *App {
	t.Helper()
	application := newTestApp(t, snapshot, newStaticStopPolicy(time.Second))
	application.registrar = registrar
	application.registrarTimeout = 100 * time.Millisecond
	application.instance = &registry.ServiceInstance{ID: "registered"}
	return application
}

func TestRegisterAndStopDeregisterStoredInstanceOnce(t *testing.T) {
	underlying := new(registrarCallFake)
	application := newRegistrarTestApp(t, underlying, appSnapshot{})
	if err := application.register(application.instance); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := application.Stop(); err != nil {
			t.Fatal(err)
		}
	}
	registerCalls, deregisterCalls := underlying.calls()
	if registerCalls != 1 || deregisterCalls != 1 {
		t.Fatalf("underlying calls = register %d, deregister %d", registerCalls, deregisterCalls)
	}
	underlying.mu.Lock()
	deregistered := underlying.deregistered
	underlying.mu.Unlock()
	if deregistered != application.instance {
		t.Fatalf("Deregister used instance %p, want registered instance %p", deregistered, application.instance)
	}
}

func TestRegisterFailureStopsApplicationAndAggregatesCleanupErrors(t *testing.T) {
	registerErr := errors.New("register failed")
	beforeStopErr := errors.New("before stop failed")
	afterStopErr := errors.New("after stop failed")
	underlying := &registrarCallFake{registerFn: func(context.Context, *registry.ServiceInstance) error { return registerErr }}
	spec := newApplicationTestSpec(t)
	spec.BeforeStop(func(context.Context) error { return beforeStopErr })
	spec.AfterStop(func(context.Context) error { return afterStopErr })
	runtime := newApplicationRuntime()
	registerApplicationRuntime(t, spec, runtime)
	application, err := NewApp(context.Background(), spec, applicationTestConfig(), newStaticStopPolicy(time.Second), underlying)
	if err != nil {
		t.Fatal(err)
	}
	err = application.Run()
	for _, want := range []error{registerErr, beforeStopErr, afterStopErr} {
		if !errors.Is(err, want) {
			t.Fatalf("Run error = %v, missing %v", err, want)
		}
	}
	if registerCalls, deregisterCalls := underlying.calls(); registerCalls != 1 || deregisterCalls != 0 {
		t.Fatalf("underlying calls = register %d, deregister %d", registerCalls, deregisterCalls)
	}
	if starts, stops := runtime.counts(); starts != 1 || stops != 1 {
		t.Fatalf("runtime calls = start %d, stop %d", starts, stops)
	}
}

func TestRegisterSkipsRegistrationAfterStopRequested(t *testing.T) {
	underlying := new(registrarCallFake)
	application := newRegistrarTestApp(t, underlying, appSnapshot{})
	application.requestStop()
	if err := application.register(application.instance); !errors.Is(err, errAppStopping) {
		t.Fatalf("late registration = %v, want stopping signal", err)
	}
	if registerCalls, deregisterCalls := underlying.calls(); registerCalls != 0 || deregisterCalls != 0 {
		t.Fatalf("late registration touched registry: register %d, deregister %d", registerCalls, deregisterCalls)
	}
}

func TestRegisterCompensatesWhenStopArrivesDuringRegister(t *testing.T) {
	registerEntered := make(chan struct{})
	releaseRegister := make(chan struct{})
	deregisterContext := make(chan error, 2)
	underlying := &registrarCallFake{
		registerFn: func(context.Context, *registry.ServiceInstance) error {
			close(registerEntered)
			<-releaseRegister
			return nil
		},
		deregisterFn: func(ctx context.Context, _ *registry.ServiceInstance) error {
			deregisterContext <- ctx.Err()
			return nil
		},
	}
	application := newRegistrarTestApp(t, underlying, appSnapshot{})
	result := make(chan error, 1)
	go func() { result <- application.register(application.instance) }()
	receiveSignal(t, registerEntered, "registrar Register entry")
	// Stop 在注册返回前执行，此时尚未注册，注销由注册完成后的补偿完成。
	if err := application.Stop(); err != nil {
		t.Fatal(err)
	}
	close(releaseRegister)
	if err := receiveError(t, result, "compensating Register"); !errors.Is(err, errAppStopping) {
		t.Fatalf("compensating Register error = %v", err)
	}
	if err := receiveError(t, deregisterContext, "compensating Deregister context"); err != nil {
		t.Fatalf("compensating Deregister inherited canceled context: %v", err)
	}
	if registerCalls, deregisterCalls := underlying.calls(); registerCalls != 1 || deregisterCalls != 1 {
		t.Fatalf("compensation calls = register %d, deregister %d", registerCalls, deregisterCalls)
	}
}

// Stop 与注册完成交错时注销恰好执行一次。
func TestRegisterAndConcurrentStopDeregisterExactlyOnce(t *testing.T) {
	for range 200 {
		underlying := new(registrarCallFake)
		application := newRegistrarTestApp(t, underlying, appSnapshot{})
		var wg sync.WaitGroup
		wg.Go(func() { _ = application.register(application.instance) })
		wg.Go(func() { _ = application.Stop() })
		wg.Wait()
		registerCalls, deregisterCalls := underlying.calls()
		if registerCalls == 1 && deregisterCalls != 1 || registerCalls == 0 && deregisterCalls != 0 {
			t.Fatalf("calls = register %d, deregister %d", registerCalls, deregisterCalls)
		}
	}
}

func TestDeregisterFailureIsReturnedWithoutBlockingRuntimeStop(t *testing.T) {
	initializeRuntimeSignals(t)
	synctest.Test(t, func(t *testing.T) {
		deregisterErr := errors.New("registry unavailable")
		underlying := &registrarCallFake{deregisterFn: func(context.Context, *registry.ServiceInstance) error { return deregisterErr }}
		spec := newApplicationTestSpec(t)
		spec.AddSignals(syscall.SIGUSR2)
		runtime := newApplicationRuntime()
		registerApplicationRuntime(t, spec, runtime)
		application, err := NewApp(context.Background(), spec, applicationTestConfig(), newStaticStopPolicy(time.Second), underlying)
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- application.Run() }()
		synctest.Wait()
		if err := application.Stop(); err != nil {
			t.Fatalf("Stop = %v, deregistration failure must not fail BeforeStop", err)
		}
		if err := <-result; !errors.Is(err, deregisterErr) {
			t.Fatalf("Run = %v, want %v", err, deregisterErr)
		}
		if starts, stops := runtime.counts(); starts != 1 || stops != 1 {
			t.Fatalf("runtime calls = start %d, stop %d", starts, stops)
		}
	})
}

func TestStopBeforeRegisterDoesNotDeregister(t *testing.T) {
	underlying := new(registrarCallFake)
	application := newRegistrarTestApp(t, underlying, appSnapshot{})
	if err := application.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, deregisterCalls := underlying.calls(); deregisterCalls != 0 {
		t.Fatalf("unregistered Deregister calls = %d", deregisterCalls)
	}
}

func receiveSignal(t testing.TB, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func receiveError(t testing.TB, result <-chan error, description string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		return nil
	}
}

func TestNewRegistrarSelection(t *testing.T) {
	factory, cleanup, err := foundationregistry.NewFactory(testconfig.Empty(t), log.WithModule("test"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := NewRegistrar(&config_pb.App{}, factory); err == nil {
		t.Fatal("missing default registry accepted")
	}
	if _, err := NewRegistrar(&config_pb.App{Registry: "missing"}, factory); err == nil {
		t.Fatal("unknown registry accepted")
	}
	t.Run("null default", func(t *testing.T) {
		manager := testconfig.New(t, "registry", &config_pb.Registry{
			Instances: map[string]*config_pb.RegistryInstance{
				"default": {Driver: "null"},
			},
		})
		factory, cleanup, err := foundationregistry.NewFactory(manager, log.WithModule("test"))
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		registrar, err := NewRegistrar(&config_pb.App{}, factory)
		if err != nil || registrar != nil {
			t.Fatalf("null default registrar = %v, %v; want nil, nil", registrar, err)
		}
	})
}
