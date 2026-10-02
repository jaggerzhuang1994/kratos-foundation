package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	textconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/text"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/observability"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	stdgrpc "google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
	recorder.requireRecord(t, "client config update rejected", map[string]any{"event": "client.config.rejected", "level": kratoslog.LevelWarn})
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
	initial         *config_pb.Client
	tracingDisabled *bool
	tracingErr      error
	subscribeErr    error
	cancelSignal    chan struct{}

	mu         sync.Mutex
	observer   config.Observer
	cancelOnce sync.Once
	subscribed atomic.Int32
	canceled   atomic.Int32
}

func (m *capturedObserverManager) Load(key string, target any, defaults ...any) error {
	if key == "tracing.disable" {
		if m.tracingErr != nil {
			return m.tracingErr
		}
		*target.(*bool) = *defaults[0].(*bool)
		if m.tracingDisabled != nil {
			*target.(*bool) = *m.tracingDisabled
		}
		return nil
	}
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
	recorder.requireRecord(t, "client config update rejected", map[string]any{"event": "client.config.rejected", "level": kratoslog.LevelWarn})
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
	t.Run("global tracing load error does not subscribe", func(t *testing.T) {
		loadErr := errors.New("tracing config unavailable")
		manager := &capturedObserverManager{initial: new(config_pb.Client), tracingErr: loadErr}
		if _, _, err := newFactory(manager, newFakeBuilder(t), newTestLogger(discardLogger{})); !errors.Is(err, loadErr) {
			t.Fatalf("newFactory error = %v, want %v", err, loadErr)
		}
		if manager.subscribed.Load() != 0 {
			t.Fatal("failed global configuration load left a subscription")
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

func TestLoadFactoryConfigReadsGlobalTracingDefault(t *testing.T) {
	t.Parallel()
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprint(disabled), func(t *testing.T) {
			t.Parallel()
			manager := &capturedObserverManager{
				initial:         configWithTarget("orders", "http://orders.test"),
				tracingDisabled: proto.Bool(disabled),
			}
			initial, defaults, _, err := loadFactoryConfig(manager, newTestLogger(discardLogger{}))
			if err != nil {
				t.Fatal(err)
			}
			if initial.GetClients()["orders"].GetTarget() != "http://orders.test" || defaults.TracingDisabled != disabled {
				t.Fatalf("client config or global tracing default lost: defaults=%+v", defaults)
			}
		})
	}
}

func TestFactoryModuleLoggingAppliesToRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte("{}")); err != nil {
			t.Error(err)
		}
	}))
	defer upstream.Close()
	logger, recorder := newRecordingLogger()
	configuration := &config_pb.Client{Clients: map[string]*config_pb.ClientOption{"upstream": {Protocol: config_pb.Protocol_HTTP.Enum(), Target: upstream.URL}}}
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
	if len(recorder.records) == 0 {
		t.Fatalf("records = %v, want records=true", recorder.records)
	}
}

func TestNewFactoryRejectsInvalidSREBeforeFirstRequest(t *testing.T) {
	builder := newTestRealBuilder(t, nil)
	config := configWithTarget("orders", "127.0.0.1:1")
	config.Clients["orders"].CircuitBreaker = &config_pb.Middleware_CircuitBreaker{
		Enable: proto.Bool(true), Sre: &config_pb.Middleware_CircuitBreaker_SREBreaker{Bucket: proto.Int32(0)},
	}
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

type fakeBuilder struct {
	validateFn func(*config_pb.Client, observability.Defaults) error
	buildFn    func(context.Context, clientSpec) (clientResult, error)
}

func (b *fakeBuilder) validateConfig(config *config_pb.Client, defaults observability.Defaults) error {
	if b.validateFn != nil {
		return b.validateFn(config, defaults)
	}
	return config.ValidateAll()
}

func (b *fakeBuilder) build(ctx context.Context, spec clientSpec) (clientResult, error) {
	return b.buildFn(ctx, spec)
}

func fakeHTTPResult(closes *atomic.Int32) clientResult {
	return clientResult{
		httpClient: new(kratoshttp.Client),
		closeFn: func() error {
			closes.Add(1)
			return nil
		},
	}
}

func fakeGRPCResult(closes *atomic.Int32) clientResult {
	return clientResult{
		grpcClient: new(stdgrpc.ClientConn),
		closeFn: func() error {
			closes.Add(1)
			return nil
		},
	}
}

func setAtomicMaximum(maximum *atomic.Int32, candidate int32) {
	for current := maximum.Load(); candidate > current; current = maximum.Load() {
		if maximum.CompareAndSwap(current, candidate) {
			return
		}
	}
}

func fakeResultForSpec(spec clientSpec, closes *atomic.Int32) clientResult {
	if spec.protocol == config_pb.Protocol_GRPC {
		return fakeGRPCResult(closes)
	}
	return fakeHTTPResult(closes)
}

type acquiredResult struct {
	httpClient *kratoshttp.Client
	grpcClient *stdgrpc.ClientConn
	release    func()
	err        error
}

const testWaitTimeout = 5 * time.Second

type testLogger struct {
	logger kratoslog.Logger
	*kratoslog.Helper
}

func newTestLogger(logger kratoslog.Logger) foundationlog.Logger {
	return &testLogger{
		logger: logger,
		Helper: kratoslog.NewHelper(logger),
	}
}

func (l *testLogger) Log(level kratoslog.Level, keyvals ...any) error {
	return l.logger.Log(level, keyvals...)
}

func (l *testLogger) With(keyvals ...any) foundationlog.Logger {
	return newTestLogger(kratoslog.With(l.logger, keyvals...))
}

func (l *testLogger) WithModule(module string) foundationlog.Logger {
	return l.With("module", module)
}

func (l *testLogger) WithContext(ctx context.Context) foundationlog.Logger {
	return newTestLogger(kratoslog.WithContext(ctx, l.logger))
}

func (l *testLogger) WithCallerDepth(depth int) foundationlog.Logger {
	return l.With("caller", kratoslog.Caller(depth))
}

func (l *testLogger) WithFilterKeys(keys ...string) foundationlog.Logger {
	return newTestLogger(kratoslog.NewFilter(l.logger, kratoslog.FilterKey(keys...)))
}

func receiveWithin[T any](t testing.TB, values <-chan T, description string) T {
	t.Helper()
	timer := time.NewTimer(testWaitTimeout)
	defer timer.Stop()
	select {
	case value := <-values:
		return value
	case <-timer.C:
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}

func idempotentClose(channel chan struct{}) func() {
	var once sync.Once
	return func() {
		once.Do(func() { close(channel) })
	}
}

func acquireClientAsync(f *factory, ctx context.Context, name string) <-chan acquiredResult {
	result := make(chan acquiredResult, 1)
	go func() {
		httpClient, grpcClient, release, err := f.AcquireClient(ctx, name)
		result <- acquiredResult{
			httpClient: httpClient,
			grpcClient: grpcClient,
			release:    release,
			err:        err,
		}
	}()
	return result
}

// updateFactoryConfig mirrors the production subscription callback while keeping
// direct test orchestration out of the factory's business API.
func updateFactoryConfig(f *factory, next *config_pb.Client) error {
	if err := f.beginActivity(); err != nil {
		return err
	}
	defer f.endActivity()
	return f.updateConfigActive(next)
}

type observedDoneContext struct {
	context.Context
	once     sync.Once
	observed chan<- struct{}
}

func (c *observedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { c.observed <- struct{}{} })
	return c.Context.Done()
}

type versionSnapshot struct {
	version    *clientVersion
	revision   uint64
	protocol   config_pb.Protocol
	target     string
	httpClient *kratoshttp.Client
	grpcClient *stdgrpc.ClientConn
}

func configWithTarget(name, target string) *config_pb.Client {
	return configWithTargets(map[string]string{name: target})
}

func configWithTargets(targets map[string]string) *config_pb.Client {
	clients := make(map[string]*config_pb.ClientOption, len(targets))
	for name, target := range targets {
		protocol := config_pb.Protocol_HTTP
		clients[name] = &config_pb.ClientOption{Protocol: &protocol, Target: target}
	}
	return &config_pb.Client{Clients: clients}
}

func newFactoryState(
	t testing.TB,
	initial *config_pb.Client,
	builder clientBuilder,
	logger foundationlog.Logger,
) *factory {
	t.Helper()
	if initial == nil {
		initial = new(config_pb.Client)
	}
	if fake, ok := builder.(*fakeBuilder); ok && fake.validateFn == nil {
		fake.validateFn = newTestRealBuilder(t, nil).validateConfig
	}
	if err := builder.validateConfig(initial, observability.Defaults{}); err != nil {
		t.Fatal(err)
	}
	f := &factory{
		logger:         logger,
		builder:        builder,
		config:         proto.CloneOf(initial),
		slots:          make(map[string]*clientSlot),
		leases:         make(map[*clientVersion]string),
		cleanupTimeout: 30 * time.Second,
	}
	for name, option := range initial.GetClients() {
		f.slots[name] = &clientSlot{
			name: name,
			current: &clientVersion{
				revision: 1,
				spec:     newClientSpec(name, option, initial, observability.Defaults{}),
			},
		}
	}
	return f
}

func newTestFactory(t testing.TB, builder clientBuilder) *factory {
	t.Helper()
	return newFactoryState(
		t,
		new(config_pb.Client),
		builder,
		newTestLogger(discardLogger{}),
	)
}

func newConfiguredTestFactory(
	t testing.TB,
	initial *config_pb.Client,
	buildFn func(context.Context, clientSpec) (clientResult, error),
) *factory {
	t.Helper()
	return newFactoryState(
		t,
		initial,
		&fakeBuilder{buildFn: buildFn},
		newTestLogger(discardLogger{}),
	)
}

func newReadyTestFactory(t testing.TB, names ...string) *factory {
	t.Helper()
	targets := make(map[string]string, len(names))
	for _, name := range names {
		targets[name] = "http://127.0.0.1:1"
	}
	f := newConfiguredTestFactory(t, configWithTargets(targets), func(context.Context, clientSpec) (clientResult, error) {
		return fakeHTTPResult(new(atomic.Int32)), nil
	})
	for _, name := range names {
		_, _, release, err := f.AcquireClient(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	return f
}

func newReadyConfiguredTestFactory(
	t testing.TB,
	initial *config_pb.Client,
	closes *atomic.Int32,
) *factory {
	t.Helper()
	f := newConfiguredTestFactory(t, initial, func(_ context.Context, spec clientSpec) (clientResult, error) {
		return fakeResultForSpec(spec, closes), nil
	})
	for name := range initial.GetClients() {
		_, _, release, err := f.AcquireClient(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	return f
}

func newReadyTestFactoryWithCloseCount(
	t testing.TB,
	name string,
	closes *atomic.Int32,
) *factory {
	t.Helper()
	return newReadyConfiguredTestFactory(
		t,
		configWithTarget(name, "http://127.0.0.1:1"),
		closes,
	)
}

func currentReferences(f *factory, name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.slots[name].current.references
}

func currentSnapshot(f *factory, name string) versionSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	version := f.slots[name].current
	return versionSnapshot{
		version:    version,
		revision:   version.revision,
		protocol:   version.spec.protocol,
		target:     version.spec.target,
		httpClient: version.client.httpClient,
		grpcClient: version.client.grpcClient,
	}
}

type logRecord struct {
	level   kratoslog.Level
	keyvals []any
}

type logRecorder struct {
	mu      sync.Mutex
	records []logRecord
}

type recordingLogger struct {
	recorder *logRecorder
}

func (l *recordingLogger) Log(level kratoslog.Level, keyvals ...any) error {
	l.recorder.mu.Lock()
	defer l.recorder.mu.Unlock()
	l.recorder.records = append(l.recorder.records, logRecord{
		level:   level,
		keyvals: append([]any(nil), keyvals...),
	})
	return nil
}

func newRecordingLogger() (foundationlog.Logger, *logRecorder) {
	recorder := new(logRecorder)
	return newTestLogger(&recordingLogger{recorder: recorder}), recorder
}

func (r *logRecorder) requireRecord(t testing.TB, message string, fields map[string]any) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if r.hasRecord(message, fields) {
			return
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			r.mu.Lock()
			records := append([]logRecord(nil), r.records...)
			r.mu.Unlock()
			t.Fatalf("log message %q with fields %v not found in %v", message, fields, records)
		}
	}
}

func (r *logRecorder) hasRecord(message string, fields map[string]any) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, record := range r.records {
		values := make(map[string]any, len(record.keyvals)/2)
		values["level"] = record.level
		for index := 0; index+1 < len(record.keyvals); index += 2 {
			key, ok := record.keyvals[index].(string)
			if ok {
				values[key] = record.keyvals[index+1]
			}
		}
		if values[kratoslog.DefaultMessageKey] != message {
			continue
		}
		matched := true
		for key, want := range fields {
			if !reflect.DeepEqual(values[key], want) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// waitForFactoryClosed 观察已有关闭状态，不为测试在生产对象中保留额外 Context。
func waitForFactoryClosed(t testing.TB, f *factory) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		f.mu.Lock()
		closed := f.closed
		f.mu.Unlock()
		if closed {
			return
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatal("timed out waiting for factory close")
		}
	}
}

// newFactory 为资源池测试注入构造替身，沿用生产配置加载与组装入口。
func newFactory(manager config.Manager, builder clientBuilder, logger foundationlog.Logger) (Factory, func(), error) {
	initial, defaults, logger, err := loadFactoryConfig(manager, logger)
	if err != nil {
		return nil, nil, err
	}
	return newConfiguredFactory(manager, builder, logger, initial, defaults)
}

func (l *testLogger) WithLevel(kratoslog.Level) foundationlog.Logger { return l }
