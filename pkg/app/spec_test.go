package app

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/registry"
)

type testRuntime struct{}

func (*testRuntime) Start(context.Context) error { return nil }
func (*testRuntime) Stop(context.Context) error  { return nil }

type testAppInfo struct{}

func (testAppInfo) ID() string                  { return "test-id" }
func (testAppInfo) Name() string                { return "test-name" }
func (testAppInfo) Version() string             { return "test-version" }
func (testAppInfo) Metadata() map[string]string { return map[string]string{"source": "info"} }

type testRegistrar struct{}

func (testRegistrar) Register(context.Context, *registry.ServiceInstance) error   { return nil }
func (testRegistrar) Deregister(context.Context, *registry.ServiceInstance) error { return nil }

func TestFreezeAppliesContextOutsideLockAndPreservesBase(t *testing.T) {
	type contextKey string
	const key contextKey = "base"

	spec := NewSpec()
	if err := spec.AddContext(func(ctx context.Context) context.Context {
		if err := spec.AddMetadata(map[string]string{"late": "value"}); !errors.Is(err, ErrSpecFrozen) {
			t.Fatalf("AddMetadata error = %v, want ErrSpecFrozen", err)
		}
		return context.WithValue(ctx, contextKey("decorated"), "value")
	}); err != nil {
		t.Fatal(err)
	}

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
	if err := spec.AddContext(func(context.Context) context.Context { return nil }); err != nil {
		t.Fatal(err)
	}

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
	if err := spec.AddContext(func(ctx context.Context) context.Context {
		calls = append(calls, "first")
		return context.WithValue(ctx, key, "first")
	}); err != nil {
		t.Fatal(err)
	}
	if err := spec.AddContext(func(ctx context.Context) context.Context {
		calls = append(calls, "second")
		if got := ctx.Value(key); got != "first" {
			t.Fatalf("second context input = %v, want first", got)
		}
		return context.WithValue(ctx, key, "second")
	}); err != nil {
		t.Fatal(err)
	}

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
	if err := spec.RegisterAppInfo(info); err != nil {
		t.Fatal(err)
	}
	if err := spec.AddMetadata(map[string]string{"source": "explicit", "extra": "value"}); err != nil {
		t.Fatal(err)
	}

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
	if err := spec.AddMetadata(metadata); err != nil {
		t.Fatal(err)
	}
	if err := spec.AddEndpoints(endpoints...); err != nil {
		t.Fatal(err)
	}
	if err := spec.AddSignals(signals...); err != nil {
		t.Fatal(err)
	}

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
		call func() error
	}{
		{name: "runtime", call: func() error { return spec.RegisterRuntime(&testRuntime{}) }},
		{name: "app info", call: func() error { return spec.RegisterAppInfo(testAppInfo{}) }},
		{name: "logger", call: func() error { return spec.RegisterLogger(kratoslog.NewStdLogger(nil)) }},
		{name: "registrar", call: func() error { return spec.RegisterRegistrar(testRegistrar{}) }},
		{name: "context", call: func() error {
			return spec.AddContext(func(ctx context.Context) context.Context { return ctx })
		}},
		{name: "metadata", call: func() error { return spec.AddMetadata(map[string]string{"key": "value"}) }},
		{name: "endpoints", call: func() error { return spec.AddEndpoints(endpoint) }},
		{name: "signals", call: func() error { return spec.AddSignals() }},
		{name: "before start", call: func() error { return spec.BeforeStart(func(context.Context) error { return nil }) }},
		{name: "after start", call: func() error { return spec.AfterStart(func(context.Context) error { return nil }) }},
		{name: "before stop", call: func() error { return spec.BeforeStop(func(context.Context) error { return nil }) }},
		{name: "after stop", call: func() error { return spec.AfterStop(func(context.Context) error { return nil }) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, ErrSpecFrozen) {
				t.Fatalf("error = %v, want ErrSpecFrozen", err)
			}
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
		if err := spec.AddContext(decorate); err != nil {
			t.Fatal(err)
		}
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
		if err := spec.RegisterRuntime(runtime); err != nil {
			t.Fatal(err)
		}
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
