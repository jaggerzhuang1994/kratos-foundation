package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
)

func newFakeBuilder(t testing.TB) *fakeBuilder {
	t.Helper()
	return &fakeBuilder{
		validateFn: newTestRealBuilder(t, nil).validateConfig,
		buildFn: func(_ context.Context, spec clientSpec) (clientResult, error) {
			return fakeResultForSpec(spec, new(atomic.Int32)), nil
		},
	}
}

func waitForTarget(t testing.TB, f *factory, name, target string) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		f.mu.Lock()
		slot := f.slots[name]
		got := ""
		if slot != nil {
			got = slot.current.spec.target
		}
		f.mu.Unlock()
		if got == target {
			return
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatalf("client %q target = %q, want %q", name, got, target)
		}
	}
}

func waitForLogLevel(t testing.TB, f *factory, level string) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		f.mu.Lock()
		got := f.config.GetLog().GetLevel()
		f.mu.Unlock()
		if got == level {
			return
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatalf("client log level = %q, want %q", got, level)
		}
	}
}

func newReadyFactoryWithCleanup(
	t testing.TB,
	name string,
) (Factory, func(), *atomic.Int32) {
	t.Helper()
	closes := new(atomic.Int32)
	f := newReadyTestFactoryWithCloseCount(t, name, closes)
	return f, f.cleanup(func() {}), closes
}

func newFactoryWithoutSubscription(
	t testing.TB,
	builder clientBuilder,
) (*factory, func()) {
	t.Helper()
	f := newTestFactory(t, builder)
	return f, f.cleanup(func() {})
}

func TestNewFactoryAppliesConfigSubscription(t *testing.T) {
	initial := configWithTarget("orders", "http://127.0.0.1:1")
	source := testconfig.NewMutableSource(t, "client", initial)
	manager, closeManager, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeManager)
	logger, _ := newRecordingLogger()
	clientFactory, cleanup, err := newFactory(manager, newFakeBuilder(t), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	source.Update(t, configWithTarget("orders", "http://127.0.0.1:2"))
	waitForTarget(t, clientFactory.(*factory), "orders", "http://127.0.0.1:2")
}

func TestNewFactoryLogOnlyUpdateKeepsClientVersion(t *testing.T) {
	initial := configWithTarget("orders", "http://127.0.0.1:1")
	source := testconfig.NewMutableSource(t, "client", initial)
	manager, closeManager, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeManager)
	logger, recorder := newRecordingLogger()
	clientFactory, cleanup, err := newFactory(manager, newFakeBuilder(t), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	f := clientFactory.(*factory)
	before := currentSnapshot(f, "orders")
	next := proto.CloneOf(initial)
	level := "error"
	next.Log = &config_pb.ModuleLog{Level: &level}
	source.Update(t, next)
	waitForLogLevel(t, f, level)
	if after := currentSnapshot(f, "orders"); after != before {
		t.Fatal("log-only update replaced client state")
	}
	recorder.requireRecord(t, "client log config changed; restart required", nil)
}

func TestNewFactoryObserverRejectsInvalidUpdates(t *testing.T) {
	initial := configWithTarget("orders", "http://127.0.0.1:1")
	source := testconfig.NewMutableSource(t, "client", initial)
	manager, closeManager, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeManager)
	logger, recorder := newRecordingLogger()
	clientFactory, cleanup, err := newFactory(manager, newFakeBuilder(t), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	f := clientFactory.(*factory)
	before := currentSnapshot(f, "orders")

	invalid := configWithTarget("orders", "http://127.0.0.1:2")
	protocol := config_pb.Protocol(99)
	invalid.Clients["orders"].Protocol = &protocol
	source.Update(t, invalid)
	recorder.requireRecord(t, "client config update rejected", nil)
	if got := countLogRecords(recorder, "client config update rejected", map[string]any{
		"error": ErrInvalidProtocol,
	}); got != 1 {
		t.Fatalf("invalid-protocol rejection log count = %d, want 1", got)
	}
	if after := currentSnapshot(f, "orders"); after != before {
		t.Fatal("invalid observer update changed client state")
	}
}

type capturedObserverManager struct {
	initial      *config_pb.Client
	subscribeErr error
	cancelSignal chan struct{}

	mu         sync.Mutex
	observer   config.Observer
	cancelOnce sync.Once
	subscribed atomic.Int32
	canceled   atomic.Int32
}

func (m *capturedObserverManager) Load(_ string, target any, _ ...any) error {
	destination, ok := target.(*config_pb.Client)
	if !ok {
		return fmt.Errorf("load target has type %T", target)
	}
	proto.Reset(destination)
	proto.Merge(destination, m.initial)
	return nil
}

func (m *capturedObserverManager) Subscribe(
	_ string,
	_ any,
	observer config.Observer,
	_ ...any,
) (func(), error) {
	m.subscribed.Add(1)
	cancel := func() {
		m.canceled.Add(1)
		if m.cancelSignal != nil {
			m.cancelOnce.Do(func() { close(m.cancelSignal) })
		}
	}
	if m.subscribeErr != nil {
		return cancel, m.subscribeErr
	}
	m.mu.Lock()
	m.observer = observer
	m.mu.Unlock()
	observer("client", proto.CloneOf(m.initial), nil)
	return cancel, nil
}

func (m *capturedObserverManager) notify(value any, err error) {
	m.mu.Lock()
	observer := m.observer
	m.mu.Unlock()
	observer("client", value, err)
}

func TestNewFactoryObserverRejectsWrongTypeAndIgnoresClosedFactory(t *testing.T) {
	manager := &capturedObserverManager{initial: new(config_pb.Client)}
	logger, recorder := newRecordingLogger()
	_, cleanup, err := newFactory(manager, newFakeBuilder(t), logger)
	if err != nil {
		t.Fatal(err)
	}

	manager.notify("not a client config", nil)
	recorder.requireRecord(t, "client config update rejected", nil)
	errorsRecorded := recordedLogErrors(recorder, "client config update rejected")
	if len(errorsRecorded) != 1 || errorsRecorded[0] == nil {
		t.Fatalf("wrong-type rejection errors = %v, want one non-nil error", errorsRecorded)
	}
	deliveryErr := errors.New("config delivery failed")
	manager.notify(new(config_pb.Client), deliveryErr)
	recorder.requireRecord(t, "client config update rejected", map[string]any{
		"error": deliveryErr,
	})
	before := countLogRecords(recorder, "client config update rejected", nil)
	cleanup()
	manager.notify(new(config_pb.Client), nil)
	manager.notify(nil, ErrFactoryClosed)
	if after := countLogRecords(recorder, "client config update rejected", nil); after != before {
		t.Fatalf("closed-factory observer rejection logs = %d, want %d", after, before)
	}
	if got := manager.canceled.Load(); got != 1 {
		t.Fatalf("subscription cancellation count = %d, want 1", got)
	}
}

func TestNewFactoryConstructionFailuresDoNotLeaveSubscription(t *testing.T) {
	t.Run("module logger configuration fails before subscribe", func(t *testing.T) {
		level := "verbose"
		manager := &capturedObserverManager{initial: &config_pb.Client{
			Log: &config_pb.ModuleLog{Level: &level},
		}}
		logger, _ := newRecordingLogger()
		if _, _, err := newFactory(manager, newFakeBuilder(t), logger); err == nil {
			t.Fatal("newFactory accepted invalid module log config")
		}
		if got := manager.subscribed.Load(); got != 0 {
			t.Fatalf("subscription count = %d, want 0", got)
		}
	})

	t.Run("subscribe error cancels returned subscription", func(t *testing.T) {
		subscribeErr := errors.New("subscribe failed")
		manager := &capturedObserverManager{
			initial:      new(config_pb.Client),
			subscribeErr: subscribeErr,
		}
		logger, _ := newRecordingLogger()
		if _, _, err := newFactory(manager, newFakeBuilder(t), logger); !errors.Is(err, subscribeErr) {
			t.Fatalf("newFactory error = %v, want %v", err, subscribeErr)
		}
		if got := manager.subscribed.Load(); got != 1 {
			t.Fatalf("subscription count = %d, want 1", got)
		}
		if got := manager.canceled.Load(); got != 1 {
			t.Fatalf("subscription cancellation count = %d, want 1", got)
		}
	})
}

func TestFactoryModuleLoggingAppliesToRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte("{}")); err != nil {
			t.Error(err)
		}
	}))
	defer upstream.Close()
	for _, tt := range []struct {
		name        string
		config      *config_pb.ModuleLog
		wantRecords bool
	}{
		{"enabled", &config_pb.ModuleLog{}, true},
		{"disabled", &config_pb.ModuleLog{Disable: proto.Bool(true)}, false},
		{"error level", &config_pb.ModuleLog{Level: proto.String("error")}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			logger, recorder := newRecordingLogger()
			configuration := &config_pb.Client{Log: tt.config, Clients: map[string]*config_pb.ClientOption{"upstream": {Protocol: config_pb.Protocol_HTTP.Enum(), Target: upstream.URL}}}
			f, cleanup, err := NewFactory(testconfig.New(t, "client", configuration), logger, appinfo.New("test"), newTestTracingProvider(t), newTestMetricsProvider(t), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			client, _, release, err := f.AcquireClient(context.Background(), "upstream")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if err := client.Invoke(context.Background(), http.MethodGet, "/", nil, &struct{}{}); err != nil {
				t.Fatal(err)
			}
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			if got := len(recorder.records) > 0; got != tt.wantRecords {
				t.Fatalf("records = %v, want records=%t", recorder.records, tt.wantRecords)
			}
		})
	}
}

func TestNewFactoryRejectsInvalidSREBeforeFirstRequest(t *testing.T) {
	builder := newTestRealBuilder(t, nil)
	config := configWithTarget("orders", "127.0.0.1:1")
	config.Clients["orders"].Middleware = &config_pb.ClientMiddleware{CircuitBreaker: &config_pb.Middleware_CircuitBreaker{
		Enable: proto.Bool(true), Sre: &config_pb.Middleware_CircuitBreaker_SREBreaker{Bucket: proto.Int32(0)},
	}}
	factory, cleanup, err := NewFactory(&capturedObserverManager{initial: config}, builder.logger, appinfo.New("test"), builder.tracing, builder.metrics, nil)
	if cleanup != nil {
		cleanup()
	}
	if err == nil || factory != nil {
		t.Fatalf("NewFactory = (%v, %v), want configuration error", factory, err)
	}
}
