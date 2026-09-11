package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	textconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/text"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
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

// TestIntegrationConfiguredHTTPFactory 从真实文本配置构造工厂，经本地 HTTP 验证调用结果。
func TestIntegrationConfiguredHTTPFactory(t *testing.T) {
	for _, tt := range []struct {
		name, clientName, method, path string
		status                         int
		canceled                       bool
	}{
		{"get primary", "orders", http.MethodGet, "/echo", 200, false},
		{"named backend", "audit", http.MethodGet, "/echo", 200, false},
		{"query escaping", "orders", http.MethodGet, "/echo?q=a%2Bb%20c", 200, false},
		{"json post", "orders", http.MethodPost, "/echo", 200, false},
		{"business rejection", "orders", http.MethodPost, "/reject", 422, false},
		{"missing route", "audit", http.MethodGet, "/missing", 404, false},
		{"canceled request", "orders", http.MethodGet, "/echo", 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			type echo struct {
				Backend string `json:"backend"`
				Method  string `json:"method"`
				Query   string `json:"query"`
				Value   string `json:"value"`
			}
			var requests atomic.Int32
			options := map[string]any{}
			for _, backend := range []string{"orders", "audit"} {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path == "/reject" || r.URL.Path == "/missing" {
						code := 422
						if r.URL.Path == "/missing" {
							code = 404
						}
						w.WriteHeader(code)
						if err := json.NewEncoder(w).Encode(map[string]any{"code": code, "reason": "BUSINESS_ERROR", "message": "request rejected"}); err != nil {
							t.Error(err)
						}
						return
					}
					result := echo{Backend: backend, Method: r.Method, Query: r.URL.Query().Get("q")}
					if r.Method == http.MethodPost {
						var body map[string]string
						if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
							t.Error(err)
							w.WriteHeader(400)
							return
						}
						result.Value = body["value"]
					}
					if err := json.NewEncoder(w).Encode(result); err != nil {
						t.Error(err)
					}
				}))
				t.Cleanup(server.Close)
				options[backend] = map[string]any{"protocol": "HTTP", "target": server.URL}
			}
			content, err := json.Marshal(map[string]any{"client": map[string]any{"clients": options}})
			if err != nil {
				t.Fatal(err)
			}
			source, err := textconfig.NewSource("clients.json", config.JSONFormat, string(content))
			if err != nil {
				t.Fatal(err)
			}
			manager, closeConfig, err := config.NewManager(config.NewSources(source))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(closeConfig)
			factory, cleanup, err := NewFactory(manager, newTestLogger(discardLogger{}), appinfo.New("integration"), newTestTracingProvider(t), newTestMetricsProvider(t), nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			httpClient, grpcClient, release, err := factory.AcquireClient(ctx, tt.clientName)
			if err != nil {
				t.Fatal(err)
			}
			if release == nil {
				t.Fatal("missing release")
			}
			t.Cleanup(release)
			if httpClient == nil || grpcClient != nil {
				t.Fatal("unexpected protocol client")
			}
			if tt.canceled {
				cancel()
			}
			var got echo
			var input any
			if tt.method == http.MethodPost {
				input = map[string]string{"value": "订单 + 100%"}
			}
			err = httpClient.Invoke(ctx, tt.method, tt.path, input, &got)
			switch {
			case tt.canceled:
				if !errors.Is(err, context.Canceled) || requests.Load() != 0 {
					t.Fatalf("canceled request: err=%v requests=%d", err, requests.Load())
				}
			case tt.status != 200:
				if foundationerrors.Code(err) != tt.status || foundationerrors.Reason(err) != "BUSINESS_ERROR" {
					t.Fatalf("business error = %v", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				want := echo{Backend: tt.clientName, Method: tt.method}
				if strings.Contains(tt.path, "?") {
					want.Query = "a+b c"
				}
				if tt.method == http.MethodPost {
					want.Value = "订单 + 100%"
				}
				if got != want {
					t.Fatalf("response = %+v, want %+v", got, want)
				}
			}
			if !tt.canceled && requests.Load() != 1 {
				t.Fatalf("requests = %d, want 1", requests.Load())
			}
			// 操作租约先归还，再关闭工厂；重复释放不应破坏资源计数。
			release()
			release()
			cleanup()
			_, _, lateRelease, err := factory.AcquireClient(t.Context(), tt.clientName)
			if lateRelease != nil {
				lateRelease()
				t.Error("closed factory returned lease")
			}
			if !errors.Is(err, ErrFactoryClosed) {
				t.Fatalf("acquire after cleanup = %v", err)
			}
		})
	}
}
