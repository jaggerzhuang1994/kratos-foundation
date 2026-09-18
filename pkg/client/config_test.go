package client

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestFactoryUpdateKeepsOldClientUntilFinalRelease(t *testing.T) {
	t.Parallel()
	closeCount := new(atomic.Int32)
	factory := newReadyTestFactoryWithCloseCount(t, "orders", closeCount)
	logger, recorder := newRecordingLogger()
	factory.logger = logger
	_, _, release, err := factory.AcquireClient(context.Background(), "orders")
	if err != nil {
		t.Fatal(err)
	}
	if err := updateFactoryConfig(factory, configWithTarget("orders", "http://127.0.0.1:2")); err != nil {
		t.Fatal(err)
	}
	if got := closeCount.Load(); got != 0 {
		t.Fatalf("close count before release = %d, want 0", got)
	}
	release()
	release()
	if got := closeCount.Load(); got != 1 {
		t.Fatalf("close count after release = %d, want 1", got)
	}
	recorder.requireRecord(t, "client closed", map[string]any{
		"client":   "orders",
		"revision": uint64(1),
		"protocol": "HTTP",
		"reason":   "config_updated",
	})
}

func TestFactoryUpdateChangesOnlyAffectedName(t *testing.T) {
	t.Parallel()
	factory := newReadyTestFactory(t, "orders", "payments")
	beforeOrders := currentSnapshot(factory, "orders")
	beforePayments := currentSnapshot(factory, "payments")
	next := configWithTargets(map[string]string{
		"orders":   "http://127.0.0.1:2",
		"payments": beforePayments.target,
	})
	if err := updateFactoryConfig(factory, next); err != nil {
		t.Fatal(err)
	}
	afterOrders := currentSnapshot(factory, "orders")
	afterPayments := currentSnapshot(factory, "payments")
	if afterOrders.revision != beforeOrders.revision+1 {
		t.Fatalf("orders revision = %d", afterOrders.revision)
	}
	if afterPayments != beforePayments {
		t.Fatal("unchanged payments state was replaced")
	}
}

func TestFactoryDeleteFallsBackToDefaultDiscovery(t *testing.T) {
	t.Parallel()
	seen := make(chan clientSpec, 1)
	factory := newConfiguredTestFactory(t, configWithTarget("orders", "http://127.0.0.1:1"), func(_ context.Context, spec clientSpec) (clientResult, error) {
		seen <- spec
		return fakeGRPCResult(new(atomic.Int32)), nil
	})
	if err := updateFactoryConfig(factory, new(config_pb.Client)); err != nil {
		t.Fatal(err)
	}
	_, _, release, err := factory.AcquireClient(context.Background(), "orders")
	if err != nil {
		t.Fatal(err)
	}
	release()
	spec := receiveWithin(t, seen, "fallback build spec")
	if spec.protocol != config_pb.Protocol_GRPC || spec.target != "discovery:///orders" {
		t.Fatalf("fallback spec = %s %q", spec.protocol, spec.target)
	}
}

func TestFactoryAbsentNameUsesDefaultDiscovery(t *testing.T) {
	t.Parallel()
	seen := make(chan clientSpec, 1)
	factory := newTestFactory(t, &fakeBuilder{buildFn: func(_ context.Context, spec clientSpec) (clientResult, error) {
		seen <- spec
		return fakeGRPCResult(new(atomic.Int32)), nil
	}})
	_, grpcClient, release, err := factory.AcquireClient(context.Background(), "orders")
	if err != nil {
		t.Fatal(err)
	}
	if grpcClient == nil {
		t.Fatal("default acquisition did not return gRPC")
	}
	release()
	spec := receiveWithin(t, seen, "default build spec")
	if spec.protocol != config_pb.Protocol_GRPC || spec.target != "discovery:///orders" {
		t.Fatalf("default spec = %s %q", spec.protocol, spec.target)
	}
}

func TestFactoryUpdateDuringBuildReturnsNewestVersion(t *testing.T) {
	t.Parallel()
	firstStarted := make(chan struct{})
	signalFirstStarted := idempotentClose(firstStarted)
	var builds, active, maximum atomic.Int32
	staleCloses := new(atomic.Int32)
	freshCloses := new(atomic.Int32)
	factory := newConfiguredTestFactory(t, configWithTarget("orders", "http://127.0.0.1:1"), func(ctx context.Context, spec clientSpec) (clientResult, error) {
		attempt := builds.Add(1)
		now := active.Add(1)
		setAtomicMaximum(&maximum, now)
		defer active.Add(-1)
		if attempt == 1 {
			signalFirstStarted()
			<-ctx.Done()
			return fakeHTTPResult(staleCloses), nil
		}
		if spec.target != "http://127.0.0.1:2" {
			t.Errorf("second build target = %q", spec.target)
		}
		return fakeHTTPResult(freshCloses), nil
	})
	logger, recorder := newRecordingLogger()
	factory.logger = logger
	result := acquireClientAsync(factory, context.Background(), "orders")
	receiveWithin(t, firstStarted, "first build to start")
	if err := updateFactoryConfig(factory, configWithTarget("orders", "http://127.0.0.1:2")); err != nil {
		t.Fatal(err)
	}
	got := receiveWithin(t, result, "updated acquisition result")
	if got.err != nil || got.httpClient == nil {
		t.Fatalf("acquisition: client=%v error=%v", got.httpClient, got.err)
	}
	got.release()
	if builds.Load() != 2 || maximum.Load() != 1 || staleCloses.Load() != 1 {
		t.Fatalf("builds=%d max=%d stale closes=%d", builds.Load(), maximum.Load(), staleCloses.Load())
	}
	recorder.requireRecord(t, "client closed", map[string]any{
		"client":   "orders",
		"revision": uint64(1),
		"protocol": "HTTP",
		"reason":   "stale_build",
	})
}

func TestFactoryRapidUpdatesBuildOnlyLatest(t *testing.T) {
	t.Parallel()
	firstStarted := make(chan struct{})
	signalFirstStarted := idempotentClose(firstStarted)
	allowFirstExit := make(chan struct{})
	unblockFirst := idempotentClose(allowFirstExit)
	t.Cleanup(unblockFirst)
	builtSpecs := make(chan clientSpec, 1)
	var builds atomic.Int32
	factory := newConfiguredTestFactory(t, configWithTarget("orders", "http://127.0.0.1:1"), func(ctx context.Context, spec clientSpec) (clientResult, error) {
		if builds.Add(1) == 1 {
			signalFirstStarted()
			<-ctx.Done()
			<-allowFirstExit
			return fakeHTTPResult(new(atomic.Int32)), nil
		}
		builtSpecs <- spec
		return fakeHTTPResult(new(atomic.Int32)), nil
	})
	result := acquireClientAsync(factory, context.Background(), "orders")
	receiveWithin(t, firstStarted, "first rapid-update build to start")
	if err := updateFactoryConfig(factory, configWithTarget("orders", "http://127.0.0.1:2")); err != nil {
		t.Fatal(err)
	}
	if err := updateFactoryConfig(factory, configWithTarget("orders", "http://127.0.0.1:3")); err != nil {
		t.Fatal(err)
	}
	unblockFirst()
	got := receiveWithin(t, result, "rapid-update acquisition result")
	if got.err != nil {
		t.Fatal(got.err)
	}
	got.release()
	if spec := receiveWithin(t, builtSpecs, "latest build spec"); spec.target != "http://127.0.0.1:3" {
		t.Fatalf("latest target = %q, want v3", spec.target)
	}
	if builds.Load() != 2 {
		t.Fatalf("build count = %d, want stale+latest", builds.Load())
	}
}

func TestFactoryConfigTransitionRetirement(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		initial           *config_pb.Client
		next              *config_pb.Client
		wantRevisionDelta uint64
		wantCloseCount    int32
	}{
		{
			name:              "unreferenced client closes immediately",
			initial:           configWithTarget("orders", "http://127.0.0.1:1"),
			next:              configWithTarget("orders", "http://127.0.0.1:2"),
			wantRevisionDelta: 1,
			wantCloseCount:    1,
		},
		{
			name:              "empty explicit option equals fallback",
			initial:           &config_pb.Client{Clients: map[string]*config_pb.ClientOption{"orders": {}}},
			next:              new(config_pb.Client),
			wantRevisionDelta: 0,
			wantCloseCount:    0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			closeCount := new(atomic.Int32)
			factory := newReadyConfiguredTestFactory(t, tt.initial, closeCount)
			before := currentSnapshot(factory, "orders")
			if err := updateFactoryConfig(factory, tt.next); err != nil {
				t.Fatal(err)
			}
			after := currentSnapshot(factory, "orders")
			if after.revision-before.revision != tt.wantRevisionDelta {
				t.Fatalf("revision delta = %d", after.revision-before.revision)
			}
			if got := closeCount.Load(); got != tt.wantCloseCount {
				t.Fatalf("close count = %d, want %d", got, tt.wantCloseCount)
			}
		})
	}
}

func TestFactoryNestedMiddlewareNoOpKeepsCachedVersion(t *testing.T) {
	closeCount := new(atomic.Int32)
	initial := configWithTarget("orders", "http://127.0.0.1:1")
	factory := newReadyConfiguredTestFactory(t, initial, closeCount)
	before := currentSnapshot(factory, "orders")

	falseValue := false
	zeroSuccess := 0.0
	noOp := configWithTarget("orders", "http://127.0.0.1:1")
	noOp.Clients["orders"].Deadline = &config_pb.Middleware_Deadline{MaxTimeout: durationpb.New(0)}
	noOp.Clients["orders"].Metadata = &config_pb.Middleware_Metadata{Disable: &falseValue}
	noOp.Clients["orders"].Logging = new(config_pb.Middleware_Logging)
	noOp.Clients["orders"].CircuitBreaker = &config_pb.Middleware_CircuitBreaker{
		Enable: &falseValue,
		Sre:    &config_pb.Middleware_CircuitBreaker_SREBreaker{Success: &zeroSuccess},
	}
	if err := updateFactoryConfig(factory, noOp); err != nil {
		t.Fatal(err)
	}
	if after := currentSnapshot(factory, "orders"); after != before {
		t.Fatalf(
			"no-op update changed version: revision=%d client_unchanged=%t version_unchanged=%t",
			after.revision,
			after.httpClient == before.httpClient,
			after.version == before.version,
		)
	}
	if got := closeCount.Load(); got != 0 {
		t.Fatalf("no-op update close count = %d, want 0", got)
	}

	trueValue := true
	changed := configWithTarget("orders", "http://127.0.0.1:1")
	changed.Clients["orders"].Logging = &config_pb.Middleware_Logging{Disable: &trueValue}
	if err := updateFactoryConfig(factory, changed); err != nil {
		t.Fatal(err)
	}
	after := currentSnapshot(factory, "orders")
	if after.version == before.version || after.revision != before.revision+1 {
		t.Fatalf("effective update revision = %d, want %d with a new version", after.revision, before.revision+1)
	}
	if got := closeCount.Load(); got != 1 {
		t.Fatalf("effective update close count = %d, want 1", got)
	}
}

func TestFactoryExplicitZeroFallbackReplacesDefaultVersion(t *testing.T) {
	closeCount := new(atomic.Int32)
	initial := configWithTarget("orders", "http://127.0.0.1:1")
	factory := newReadyConfiguredTestFactory(t, initial, closeCount)
	before := currentSnapshot(factory, "orders")

	next := configWithTarget("orders", "http://127.0.0.1:1")
	next.Clients["orders"].Deadline = &config_pb.Middleware_Deadline{FallbackTimeout: durationpb.New(0)}
	if err := updateFactoryConfig(factory, next); err != nil {
		t.Fatal(err)
	}
	after := currentSnapshot(factory, "orders")
	if after.revision != before.revision+1 || after.version == before.version {
		t.Fatalf("zero fallback kept default version: revision=%d", after.revision)
	}
	if got := closeCount.Load(); got != 1 {
		t.Fatalf("default version close count = %d, want 1", got)
	}
	if err := updateFactoryConfig(factory, initial); err != nil {
		t.Fatal(err)
	}
	if restored := currentSnapshot(factory, "orders"); restored.revision != after.revision+1 {
		t.Fatalf("removing explicit zero did not restore default: revision=%d", restored.revision)
	}
}

func TestFactoryRejectsWholeInvalidUpdate(t *testing.T) {
	t.Parallel()
	factory := newReadyTestFactory(t, "orders", "payments")
	beforeOrders := currentSnapshot(factory, "orders")
	beforePayments := currentSnapshot(factory, "payments")
	invalid := configWithTargets(map[string]string{"orders": "http://127.0.0.1:2", "payments": "http://127.0.0.1:2"})
	protocol := config_pb.Protocol(99)
	invalid.Clients["payments"].Protocol = &protocol
	err := updateFactoryConfig(factory, invalid)
	if !errors.Is(err, ErrInvalidProtocol) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidProtocol)
	}
	if currentSnapshot(factory, "orders") != beforeOrders || currentSnapshot(factory, "payments") != beforePayments {
		t.Fatal("invalid update changed current versions")
	}
}

func TestFactoryUpdateLogsCloseFailure(t *testing.T) {
	t.Parallel()
	closeErr := errors.New("close failed")
	logger, recorder := newRecordingLogger()
	factory := newConfiguredTestFactory(
		t,
		configWithTarget("orders", "http://127.0.0.1:1"),
		func(context.Context, clientSpec) (clientResult, error) {
			return clientResult{
				httpClient: new(kratoshttp.Client),
				closeFn: func() error {
					return closeErr
				},
			}, nil
		},
	)
	factory.logger = logger
	_, _, release, err := factory.AcquireClient(context.Background(), "orders")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := updateFactoryConfig(factory, configWithTarget("orders", "http://127.0.0.1:2")); err != nil {
		t.Fatal(err)
	}
	recorder.requireRecord(t, "client close failed", map[string]any{
		"client":   "orders",
		"revision": uint64(1),
		"protocol": "HTTP",
		"reason":   "config_updated",
		"error":    closeErr,
	})
}

func TestFactoryRejectsSREUpdateWithoutRetiringExistingClients(t *testing.T) {
	factory := newReadyTestFactory(t, "orders", "payments")
	beforeOrders := currentSnapshot(factory, "orders")
	beforePayments := currentSnapshot(factory, "payments")
	next := configWithTargets(map[string]string{"orders": "http://127.0.0.1:2", "payments": "http://127.0.0.1:2"})
	next.Clients["payments"].CircuitBreaker = &config_pb.Middleware_CircuitBreaker{
		Enable: proto.Bool(true), Sre: &config_pb.Middleware_CircuitBreaker_SREBreaker{Bucket: proto.Int32(0)},
	}
	if err := updateFactoryConfig(factory, next); err == nil {
		t.Error("invalid SRE update was accepted")
	}
	if currentSnapshot(factory, "orders") != beforeOrders || currentSnapshot(factory, "payments") != beforePayments {
		t.Fatal("invalid SRE update changed active client versions")
	}
}

func TestFactoryRootDefaultsUpdate(t *testing.T) {
	seen := make(chan clientSpec, 8)
	initial := &config_pb.Client{Discovery: proto.String("regional"), FallbackTimeout: durationpb.New(5 * time.Second), Clients: map[string]*config_pb.ClientOption{
		"inherited": {Target: "localhost:9000"},
		"override":  {Target: "localhost:9001", Discovery: "fixed", Deadline: &config_pb.Middleware_Deadline{FallbackTimeout: durationpb.New(2 * time.Second)}},
	}}
	f := newConfiguredTestFactory(t, initial, func(_ context.Context, spec clientSpec) (clientResult, error) {
		seen <- spec
		return fakeGRPCResult(new(atomic.Int32)), nil
	})
	acquire := func(name string) {
		t.Helper()
		_, _, release, err := f.AcquireClient(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	for _, name := range []string{"inherited", "override", "dynamic"} {
		acquire(name)
		receiveWithin(t, seen, "initial client spec")
	}
	before := currentSnapshot(f, "override")
	next := proto.CloneOf(initial)
	next.FallbackTimeout = durationpb.New(7 * time.Second)
	next.Discovery = proto.String("updated")
	if err := updateFactoryConfig(f, next); err != nil {
		t.Fatal(err)
	}
	if currentSnapshot(f, "override") != before {
		t.Fatal("overridden client rebuilt")
	}
	for _, name := range []string{"inherited", "dynamic", "new-dynamic"} {
		acquire(name)
		spec := receiveWithin(t, seen, "updated client spec")
		if spec.middleware.deadline.FallbackTimeout.AsDuration() != 7*time.Second || spec.discovery != "updated" {
			t.Fatalf("%s did not inherit updated root", name)
		}
	}
	next = proto.CloneOf(next)
	next.FallbackTimeout = nil
	if err := updateFactoryConfig(f, next); err != nil {
		t.Fatal(err)
	}
	acquire("dynamic")
	if spec := receiveWithin(t, seen, "updated client spec"); spec.middleware.GetDeadline().GetFallbackTimeout() != nil {
		t.Fatal("removed root timeout did not restore built-in default")
	}
}
