package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/registry"
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

func newRegistrarTestHarness(
	t testing.TB,
	registrar registry.Registrar,
	snapshot appSnapshot,
) (*supervisedRegistrar, *App) {
	t.Helper()
	lifecycle := newApp(snapshot, newStaticStopPolicy(time.Second))
	lifecycle.initServers(0)
	lifecycle.stop = func() error { return nil }
	return newSupervisedRegistrar(
		registrar,
		lifecycle,
		100*time.Millisecond,
	), lifecycle
}

func TestSupervisedRegistrarRegistersAndDeregistersStoredInstanceOnce(t *testing.T) {
	underlying := new(registrarCallFake)
	registrar, _ := newRegistrarTestHarness(t, underlying, appSnapshot{})
	registered := &registry.ServiceInstance{ID: "registered"}
	if err := registrar.Register(context.Background(), registered); err != nil {
		t.Fatal(err)
	}
	if err := registrar.Deregister(
		context.Background(),
		&registry.ServiceInstance{ID: "different"},
	); err != nil {
		t.Fatal(err)
	}
	if err := registrar.Deregister(context.Background(), registered); err != nil {
		t.Fatal(err)
	}
	registerCalls, deregisterCalls := underlying.calls()
	if registerCalls != 1 || deregisterCalls != 1 {
		t.Fatalf("underlying calls = register %d, deregister %d", registerCalls, deregisterCalls)
	}
	underlying.mu.Lock()
	deregistered := underlying.deregistered
	underlying.mu.Unlock()
	if deregistered != registered {
		t.Fatalf("Deregister used instance %p, want registered instance %p", deregistered, registered)
	}
}

func TestSupervisedRegistrarRegisterFailureStopsAndAggregatesCleanupErrors(t *testing.T) {
	registerErr := errors.New("register failed")
	beforeStopErr := errors.New("before stop failed")
	afterStopErr := errors.New("after stop failed")
	underlying := &registrarCallFake{registerFn: func(
		context.Context,
		*registry.ServiceInstance,
	) error {
		return registerErr
	}}
	snapshot := appSnapshot{
		beforeStop: []HookFunc{func(context.Context) error { return beforeStopErr }},
		afterStop:  []HookFunc{func(context.Context) error { return afterStopErr }},
	}
	registrar, lifecycle := newRegistrarTestHarness(t, underlying, snapshot)
	stopErr := errors.New("application stop failed")
	registrar.app.stop = func() error { return stopErr }

	err := registrar.Register(context.Background(), &registry.ServiceInstance{ID: "instance"})
	for _, want := range []error{registerErr, stopErr, beforeStopErr, afterStopErr} {
		if !errors.Is(err, want) {
			t.Fatalf("Register error = %v, missing %v", err, want)
		}
	}
	if !lifecycle.isStopping() {
		t.Fatal("register failure did not request application stop")
	}
	registerCalls, deregisterCalls := underlying.calls()
	if registerCalls != 1 || deregisterCalls != 0 {
		t.Fatalf("underlying calls = register %d, deregister %d", registerCalls, deregisterCalls)
	}
}

func TestSupervisedRegistrarSkipsRegistrationAfterStopRequested(t *testing.T) {
	underlying := new(registrarCallFake)
	registrar, lifecycle := newRegistrarTestHarness(t, underlying, appSnapshot{})
	lifecycle.requestStop()
	if err := registrar.Register(
		context.Background(),
		&registry.ServiceInstance{ID: "late"},
	); err != nil {
		t.Fatalf("late registration returned shutdown noise: %v", err)
	}
	registerCalls, deregisterCalls := underlying.calls()
	if registerCalls != 0 || deregisterCalls != 0 {
		t.Fatalf("late registration touched registry: register %d, deregister %d", registerCalls, deregisterCalls)
	}
}

func TestSupervisedRegistrarCompensatesWhenStopArrivesDuringRegister(t *testing.T) {
	registerEntered := make(chan struct{})
	releaseRegister := make(chan struct{})
	deregisterContext := make(chan error, 1)
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
	registrar, lifecycle := newRegistrarTestHarness(t, underlying, appSnapshot{})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- registrar.Register(ctx, &registry.ServiceInstance{ID: "racing"})
	}()
	receiveSignal(t, registerEntered, "registrar Register entry")
	lifecycle.requestStop()
	cancel()
	close(releaseRegister)
	if err := receiveError(t, result, "compensating Register"); err != nil {
		t.Fatalf("compensating Register error = %v", err)
	}
	if err := receiveError(t, deregisterContext, "compensating Deregister context"); err != nil {
		t.Fatalf("compensating Deregister inherited canceled context: %v", err)
	}
	registerCalls, deregisterCalls := underlying.calls()
	if registerCalls != 1 || deregisterCalls != 1 {
		t.Fatalf("compensation calls = register %d, deregister %d", registerCalls, deregisterCalls)
	}
}

func TestSupervisedRegistrarConcurrentDeregisterSharesResultAndRespectsWaiterContext(t *testing.T) {
	deregisterErr := errors.New("deregister failed")
	entered := make(chan struct{})
	release := make(chan struct{})
	underlying := &registrarCallFake{deregisterFn: func(
		context.Context,
		*registry.ServiceInstance,
	) error {
		close(entered)
		<-release
		return deregisterErr
	}}
	registrar, _ := newRegistrarTestHarness(t, underlying, appSnapshot{})
	instance := &registry.ServiceInstance{ID: "instance"}
	if err := registrar.Register(context.Background(), instance); err != nil {
		t.Fatal(err)
	}

	first := make(chan error, 1)
	go func() { first <- registrar.Deregister(context.Background(), instance) }()
	receiveSignal(t, entered, "first Deregister entry")

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := registrar.Deregister(canceledCtx, instance); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter error = %v, want context.Canceled", err)
	}
	second := make(chan error, 1)
	go func() { second <- registrar.Deregister(context.Background(), instance) }()
	close(release)
	if err := receiveError(t, first, "first Deregister"); !errors.Is(err, deregisterErr) {
		t.Fatalf("first Deregister error = %v", err)
	}
	if err := receiveError(t, second, "second Deregister"); !errors.Is(err, deregisterErr) {
		t.Fatalf("second Deregister error = %v", err)
	}
	if err := registrar.Deregister(context.Background(), instance); !errors.Is(err, deregisterErr) {
		t.Fatalf("replayed Deregister error = %v", err)
	}
	_, deregisterCalls := underlying.calls()
	if deregisterCalls != 1 {
		t.Fatalf("underlying Deregister calls = %d, want 1", deregisterCalls)
	}
}

func TestContinuingRegistrarPreservesDeregisterFailureForFinalAggregation(t *testing.T) {
	deregisterErr := errors.New("registry unavailable")
	underlying := &registrarCallFake{deregisterFn: func(
		context.Context,
		*registry.ServiceInstance,
	) error {
		return deregisterErr
	}}
	supervised, _ := newRegistrarTestHarness(t, underlying, appSnapshot{})
	continuing := &continuingRegistrar{registrar: supervised}
	instance := &registry.ServiceInstance{ID: "instance"}
	if err := continuing.Register(context.Background(), instance); err != nil {
		t.Fatal(err)
	}
	if err := continuing.Deregister(context.Background(), instance); err != nil {
		t.Fatalf("continuing Deregister blocked shutdown: %v", err)
	}
	if err := supervised.finalError(); !errors.Is(err, deregisterErr) {
		t.Fatalf("final registrar error = %v, want %v", err, deregisterErr)
	}

	shutdownErr := errors.New("additional shutdown failure")
	empty, _ := newRegistrarTestHarness(t, new(registrarCallFake), appSnapshot{})
	empty.recordShutdownError(nil)
	empty.recordShutdownError(shutdownErr)
	if err := empty.finalError(); !errors.Is(err, shutdownErr) {
		t.Fatalf("recorded shutdown error = %v, want %v", err, shutdownErr)
	}
	empty.deregisterError = deregisterErr
	empty.recordShutdownError(errors.New("ignored after primary"))
	if err := empty.finalError(); !errors.Is(err, deregisterErr) || !errors.Is(err, shutdownErr) {
		t.Fatalf("combined final error = %v", err)
	}
}

func TestSupervisedRegistrarDeregisterBeforeRegisterIsNoop(t *testing.T) {
	underlying := new(registrarCallFake)
	registrar, _ := newRegistrarTestHarness(t, underlying, appSnapshot{})
	if err := registrar.Deregister(
		context.Background(),
		&registry.ServiceInstance{ID: "never-registered"},
	); err != nil {
		t.Fatal(err)
	}
	_, deregisterCalls := underlying.calls()
	if deregisterCalls != 0 {
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
