package client

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	stdgrpc "google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type fakeBuilder struct {
	validateFn func(*config_pb.Client) error
	buildFn    func(context.Context, clientSpec) (clientResult, error)
}

func (b *fakeBuilder) validateConfig(config *config_pb.Client) error {
	if b.validateFn != nil {
		return b.validateFn(config)
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
	if err := builder.validateConfig(initial); err != nil {
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
				spec:     newClientSpec(name, option, initial),
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
	initial, logger, err := loadFactoryConfig(manager, logger)
	if err != nil {
		return nil, nil, err
	}
	return newConfiguredFactory(manager, builder, logger, initial)
}

func (l *testLogger) WithLevel(kratoslog.Level) foundationlog.Logger { return l }
