package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware"
	kratosgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	configtext "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/text"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	foundationapp "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type testServer struct {
	stops   int
	stopErr error
}

func (s *testServer) Start(context.Context) error { return nil }

func (s *testServer) Stop(context.Context) error { s.stops++; return s.stopErr }

func (s *testServer) Endpoint() (*url.URL, error) { return &url.URL{Scheme: "http", Host: "test"}, nil }

type testMetricsProvider struct{ registry *prometheus.Registry }

type endpointLog struct {
	foundationlog.Logger
	entries *[]endpointLogEntry
	fields  []any
}

type endpointLogEntry struct {
	level   string
	message string
	fields  map[string]any
}

func newEndpointLog() *endpointLog {
	entries := make([]endpointLogEntry, 0)
	return &endpointLog{entries: &entries}
}

func (l *endpointLog) WithModule(string) foundationlog.Logger { return l }

func (l *endpointLog) With(fields ...any) foundationlog.Logger {
	derived := *l
	derived.fields = append(append([]any(nil), l.fields...), fields...)
	return &derived
}

func (l *endpointLog) Info(message ...any)  { l.record("info", message...) }
func (l *endpointLog) Debug(message ...any) { l.record("debug", message...) }
func (l *endpointLog) Warn(message ...any)  { l.record("warn", message...) }
func (l *endpointLog) Error(message ...any) { l.record("error", message...) }

func (l *endpointLog) record(level string, message ...any) {
	entry := endpointLogEntry{level: level, message: fmt.Sprint(message...), fields: make(map[string]any)}
	for index := 0; index+1 < len(l.fields); index += 2 {
		key, ok := l.fields[index].(string)
		if ok {
			entry.fields[key] = l.fields[index+1]
		}
	}
	*l.entries = append(*l.entries, entry)
}

var _ metrics.Provider = testMetricsProvider{}

func (p testMetricsProvider) Meter(string, ...metric.MeterOption) metrics.Metrics {
	return noop.NewMeterProvider().Meter("test")
}

func (p testMetricsProvider) MeterProvider() metric.MeterProvider { return noop.NewMeterProvider() }

func (p testMetricsProvider) PrometheusGatherer() prometheus.Gatherer { return p.registry }

func (p testMetricsProvider) PrometheusRegisterer() prometheus.Registerer { return p.registry }

func TestStopLifecyclePreservesEndpointRunsHooksAndHonorsCancellation(t *testing.T) {
	server := &testServer{}
	var calls []string
	wrapped := withStopLifecycle(server, 0, func(context.Context) error { calls = append(calls, "before"); return nil }, nil)
	if _, ok := wrapped.(interface{ Endpoint() (*url.URL, error) }); !ok {
		t.Fatal("endpoint capability was lost")
	}
	if err := wrapped.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "before" || server.stops != 1 {
		t.Fatalf("calls=%s stops=%d", got, server.stops)
	}
	server = &testServer{stopErr: errors.New("stop failed")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := withStopLifecycle(server, time.Hour, func(context.Context) error { return errors.New("before failed") }, nil).Stop(ctx)
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "before failed") || !strings.Contains(err.Error(), "stop failed") || server.stops != 1 {
		t.Fatalf("stop err=%v stops=%d", err, server.stops)
	}
	if withStopLifecycle(server, 0, nil, nil) != server || withStopLifecycle(nil, 0, nil, nil) != nil {
		t.Fatal("unneeded lifecycle wrapper was added")
	}
}

func strp(v string) *string { return &v }

func boolp(v bool) *bool { return &v }

type runtimeTestTracingProvider struct {
	provider trace.TracerProvider
}

const runtimeTestTimeout = 5 * time.Second

func (p runtimeTestTracingProvider) Disabled() bool { return true }

func (p runtimeTestTracingProvider) TracerProvider() trace.TracerProvider {
	return p.provider
}

func (p runtimeTestTracingProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return p.provider.Tracer(name, options...)
}

func TestNewRuntimeProtocolDefaultsAndConfigPrecedence(t *testing.T) {
	for _, tt := range []struct {
		name                       string
		httpDisabled, grpcDisabled *bool
		registerGRPC               bool
		wantHTTP, wantGRPC         bool
	}{
		{name: "defaults", wantHTTP: true},
		{name: "registered grpc", registerGRPC: true, wantHTTP: true, wantGRPC: true},
		{name: "explicit grpc enable", grpcDisabled: boolp(false), wantHTTP: true, wantGRPC: true},
		{name: "explicit grpc disable", grpcDisabled: boolp(true), registerGRPC: true, wantHTTP: true},
		{name: "explicit http disable", httpDisabled: boolp(true)},
		{name: "explicit http enable", httpDisabled: boolp(false), wantHTTP: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := &config_pb.Server{
				Http: &config_pb.HttpServerOption{Disable: tt.httpDisabled},
				Grpc: &config_pb.GrpcServerOption{Disable: tt.grpcDisabled},
			}
			spec := NewSpec()
			var httpRegistered, grpcRegistered bool
			spec.HTTP().Register(func(HTTPServer) error { httpRegistered = true; return nil })
			// 配置监听选项和中间件不能代替业务服务注册，nil 注册也不能启用协议。
			spec.GRPC().Option(kratosgrpc.Address("127.0.0.1:0")).Middleware(func(next middleware.Handler) middleware.Handler { return next }).Register(nil)
			if tt.registerGRPC {
				spec.GRPC().Register(func(GRPCServer) error { grpcRegistered = true; return nil })
			}
			runtime, cleanup, err := NewRuntime(testconfig.New(t, "server", config), newRuntimeTestLogger(t),
				testMetricsProvider{registry: prometheus.NewRegistry()}, runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}, spec)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			httpServer, grpcServer := runtime.Servers()
			if (httpServer != nil) != tt.wantHTTP || (grpcServer != nil) != tt.wantGRPC {
				t.Fatalf("HTTP=%t gRPC=%t; want HTTP=%t gRPC=%t", httpServer != nil, grpcServer != nil, tt.wantHTTP, tt.wantGRPC)
			}
			if httpRegistered != tt.wantHTTP || grpcRegistered != (tt.registerGRPC && tt.wantGRPC) {
				t.Fatalf("registration callbacks: HTTP=%t gRPC=%t", httpRegistered, grpcRegistered)
			}
			if runtime.StopDelay() != 0 {
				t.Fatal("unexpected stop delay")
			}
			cleanup()
		})
	}
}

func TestRuntimeCleanupLogsListenerFailureOnceWithContext(t *testing.T) {
	closeFailure := errors.New("listener close failed")
	listener := &managedListenerStub{close: func() error { return closeFailure }}
	spec := NewSpec()
	spec.HTTP().Listener(listener)
	logger := newEndpointLog()
	_, cleanup, err := NewRuntime(
		testconfig.New(t, "server", &config_pb.Server{Http: &config_pb.HttpServerOption{Network: strp("tcp4"), Addr: strp("127.0.0.1:19090")}}),
		logger,
		testMetricsProvider{registry: prometheus.NewRegistry()},
		runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()},
		spec,
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	cleanup()
	var failures []endpointLogEntry
	for _, entry := range *logger.entries {
		if entry.fields["event"] == "server.listener.cleanup.failed" {
			failures = append(failures, entry)
		}
	}
	if len(failures) != 1 || listener.closeCalls.Load() != 1 {
		t.Fatalf("cleanup logs=%#v close calls=%d", failures, listener.closeCalls.Load())
	}
	failure := failures[0]
	loggedError, _ := failure.fields["error"].(error)
	if failure.level != "error" || failure.fields["transport"] != "http" || !errors.Is(loggedError, closeFailure) || !strings.Contains(loggedError.Error(), "business HTTP listener tcp4://127.0.0.1:19090") {
		t.Fatalf("listener cleanup context missing: %#v", failure)
	}
}

func TestNewRuntimeLogsRegisteredEndpointsInStableOrder(t *testing.T) {
	logger := newEndpointLog()
	spec := NewSpec()
	spec.HTTP().Register(func(server HTTPServer) error {
		server.Route("/orders").POST("/{id}", func(kratoshttp.Context) error { return nil })
		server.Route("/healthz").GET("/", func(kratoshttp.Context) error { return nil })
		return nil
	})
	spec.GRPC().Register(func(server GRPCServer) error {
		healthpb.RegisterHealthServer(server, health.NewServer())
		return nil
	})
	runtime, cleanup, err := NewRuntime(
		testconfig.New(t, "server", &config_pb.Server{Grpc: &config_pb.GrpcServerOption{DisableReflection: boolp(true), CustomHealth: boolp(true)}}),
		logger,
		testMetricsProvider{registry: prometheus.NewRegistry()},
		runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()},
		spec,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if runtime == nil {
		t.Fatal("runtime is nil")
	}
	var endpoints []endpointLogEntry
	var assembled int
	for _, entry := range *logger.entries {
		if entry.fields["event"] == "server.endpoint.registered" {
			if entry.level != "debug" || entry.fields["listener"] == "" {
				t.Fatalf("endpoint level or listener missing: %#v", entry)
			}
			endpoints = append(endpoints, entry)
		}
		if entry.fields["event"] == "server.assembled" && entry.level == "info" {
			_, managementListed := entry.fields["management"].([]string)
			if entry.fields["http"] != "0.0.0.0:8000" || entry.fields["grpc"] != "0.0.0.0:9000" || !managementListed {
				t.Fatalf("assembly summary addresses = %#v", entry.fields)
			}
			if _, ok := entry.fields["stop_delay"].(time.Duration); !ok {
				t.Fatalf("assembly summary stop_delay = %#v", entry.fields)
			}
			assembled++
		}
	}
	if assembled != 1 {
		t.Fatalf("assembly info summaries = %d, want 1", assembled)
	}
	wantCount := 0
	if err := runtime.http.WalkRoute(func(kratoshttp.RouteInfo) error { wantCount++; return nil }); err != nil {
		t.Fatal(err)
	}
	for _, info := range runtime.grpc.GetServiceInfo() {
		wantCount += len(info.Methods)
	}
	wantCount += 5
	if len(endpoints) != wantCount {
		t.Fatalf("endpoint logs = %v", endpoints)
	}
	wantPaths := map[string]bool{
		"/healthz":                     false,
		"/metrics":                     false,
		"/readyz":                      false,
		"/orders/{id}":                 false,
		"/grpc.health.v1.Health/Check": false,
		"/grpc.health.v1.Health/Watch": false,
	}
	keys := make([]string, 0, len(endpoints))
	for _, entry := range endpoints {
		path, _ := entry.fields["path"].(string)
		transport, _ := entry.fields["transport"].(string)
		method, _ := entry.fields["method"].(string)
		if _, ok := wantPaths[path]; ok {
			wantPaths[path] = true
		}
		keys = append(keys, transport+"\x00"+path+"\x00"+method)
	}
	for path, found := range wantPaths {
		if !found {
			t.Errorf("missing endpoint log for %s", path)
		}
	}
	for index := 1; index < len(keys); index++ {
		previous := strings.SplitN(keys[index-1], "\x00", 2)
		current := strings.SplitN(keys[index], "\x00", 2)
		if previous[0] == current[0] && previous[1] > current[1] {
			t.Fatalf("endpoint logs are not stable: %v", keys)
		}
		if previous[0] == "grpc" && current[0] == "http" {
			t.Fatalf("HTTP endpoint logged after gRPC: %v", keys)
		}
	}
}

func newRuntimeTestLogger(t *testing.T) foundationlog.Logger {
	t.Helper()
	shared, cleanup, err := testlog.New(testlog.Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: testlog.FileConfig{OutputConfig: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return shared
}

// TestIntegrationApplicationShutdown 验证真实 App 的停止信号和服务器排空边界。
// handler 由 channel 控制退出，监听关闭信号确认已进入 SDK 停机阶段。
func TestIntegrationApplicationShutdown(t *testing.T) {
	for _, protocol := range []string{"http", "grpc"} {
		for _, force := range []bool{false, true} {
			name := protocol + "/drain"
			if force {
				name = protocol + "/timeout"
			}
			t.Run(name, func(t *testing.T) {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				observedListener := &runtimeShutdownListener{Listener: listener, closed: make(chan struct{})}
				t.Cleanup(func() { _ = observedListener.Close() })
				started := make(chan context.Context, 1)
				finished := make(chan struct{})
				release := make(chan struct{})
				releaseRequest := sync.OnceFunc(func() { close(release) })
				t.Cleanup(releaseRequest)
				handle := func(ctx context.Context) error {
					defer close(finished)
					started <- ctx
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				spec := NewSpec()
				config := &config_pb.Server{}
				if protocol == "http" {
					config.Grpc = &config_pb.GrpcServerOption{Disable: proto.Bool(true)}
					spec.HTTP().Listener(observedListener).Register(
						HandleHTTP(nethttp.MethodGet, "/inflight", func(request *nethttp.Request) (any, error) {
							return map[string]string{"status": "completed"}, handle(request.Context())
						}))
				} else {
					config.Http = &config_pb.HttpServerOption{Disable: proto.Bool(true)}
					spec.GRPC().Option(kratosgrpc.Listener(observedListener), kratosgrpc.CustomHealth()).Register(func(server GRPCServer) error {
						healthpb.RegisterHealthServer(server, &runtimeShutdownHealthServer{handle: handle})
						return nil
					})
				}
				budget := runtimeTestTimeout
				if force {
					budget = 100 * time.Millisecond
				}
				manager := newRuntimeShutdownManager(t, config, budget)
				logger := newRuntimeTestLogger(t)
				runtime, cleanup, err := NewRuntime(manager, logger, testMetricsProvider{registry: prometheus.NewRegistry()},
					runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}, spec)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(cleanup)
				application := newRuntimeShutdownApp(t, manager, logger, runtime)
				runResult := make(chan error, 1)
				runFinished := make(chan struct{})
				go func() {
					defer close(runFinished)
					runResult <- application.Run()
				}()
				// 失败路径也释放 handler，再发起停止并等待 Run，避免遗留网络和 goroutine。
				t.Cleanup(func() {
					releaseRequest()
					if err := application.Stop(); err != nil {
						t.Error(err)
					}
					waitRuntimeShutdownSignal(t, runFinished, "application cleanup")
				})
				requestResult := make(chan error, 1)
				call := runtimeShutdownCall(t, protocol, listener.Addr().String())
				go func() { requestResult <- call() }()
				var requestContext context.Context
				select {
				case requestContext = <-started:
				case err := <-requestResult:
					t.Fatalf("request returned before handler started: %v", err)
				case <-time.After(runtimeTestTimeout):
					t.Fatal("request did not reach handler")
				}
				if err := application.Stop(); err != nil {
					t.Fatal(err)
				}
				waitRuntimeShutdownSignal(t, observedListener.closed, "server listener close")
				if !force {
					// 在监听已关闭的排空阶段观察有限窗口，提前取消或 Run 提前返回都会失败。
					timer := time.NewTimer(50 * time.Millisecond)
					defer timer.Stop()
					select {
					case <-requestContext.Done():
						t.Fatalf("shutdown canceled in-flight request before release: %v", requestContext.Err())
					case err := <-runResult:
						t.Fatalf("Run returned before in-flight request completed: %v", err)
					case <-timer.C:
					}
					releaseRequest()
				} else {
					waitRuntimeShutdownSignal(t, requestContext.Done(), "request cancellation after shutdown timeout")
				}
				waitRuntimeShutdownSignal(t, finished, "handler completion")
				requestErr := waitRuntimeShutdownError(t, requestResult, "request result")
				runErr := waitRuntimeShutdownError(t, runResult, "application Run")
				if !force && (requestErr != nil || runErr != nil) {
					t.Fatalf("graceful shutdown: request=%v Run=%v", requestErr, runErr)
				}
				if force {
					if requestErr == nil || !errors.Is(requestContext.Err(), context.Canceled) {
						t.Fatalf("forced shutdown: request=%v context=%v", requestErr, requestContext.Err())
					}
					// 两种 SDK 都可能在强制关闭成功后返回 nil，不能把 nil 解释为请求完成。
					if runErr != nil {
						t.Fatalf("forced %s shutdown Run=%v", protocol, runErr)
					}
				}
			})
		}
	}
}

type runtimeShutdownListener struct {
	net.Listener
	closed chan struct{}
	once   sync.Once
}

func (l *runtimeShutdownListener) Close() error {
	err := l.Listener.Close()
	l.once.Do(func() { close(l.closed) })
	return err
}

type runtimeShutdownHealthServer struct {
	healthpb.UnimplementedHealthServer
	handle func(context.Context) error
}

func (s *runtimeShutdownHealthServer) Check(ctx context.Context, _ *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	if err := s.handle(ctx); err != nil {
		return nil, err
	}
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}

type runtimeShutdownAppInfo struct{}

func (runtimeShutdownAppInfo) ID() string                  { return "shutdown-test" }
func (runtimeShutdownAppInfo) Name() string                { return "shutdown-test" }
func (runtimeShutdownAppInfo) Version() string             { return "test" }
func (runtimeShutdownAppInfo) Metadata() map[string]string { return nil }

func newRuntimeShutdownManager(t *testing.T, config *config_pb.Server, budget time.Duration) foundationconfig.Manager {
	t.Helper()
	encoded, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	// HotReloadValue 使用配置源的精确点分路径，fixture 须采用 stop_timeout 原名。
	source, err := configtext.NewSource("shutdown-test", foundationconfig.JSONFormat,
		fmt.Sprintf(`{"server":%s,"app":{"stop_timeout":"%gs"}}`, encoded, budget.Seconds()))
	if err != nil {
		t.Fatal(err)
	}
	manager, cleanup, err := foundationconfig.NewManager(foundationconfig.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return manager
}

func newRuntimeShutdownApp(t *testing.T, manager foundationconfig.Manager, logger foundationlog.Logger, runtime *Runtime, beforeStart ...foundationapp.HookFunc) *foundationapp.App {
	t.Helper()
	config, err := foundationapp.NewConfig(manager)
	if err != nil {
		t.Fatal(err)
	}
	policy, cleanup, err := foundationapp.NewStopPolicy(config, manager, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	spec := foundationapp.NewSpec()
	spec.RegisterAppInfo(runtimeShutdownAppInfo{})
	spec.RegisterLogger(logger)
	spec.BeforeStart(beforeStart...)
	httpServer, grpcServer := runtime.Servers()
	for _, server := range []foundationapp.Runtime{httpServer, grpcServer} {
		if server != nil {
			spec.RegisterRuntime(server)
		}
	}
	for _, server := range runtime.ManagementServers() {
		spec.RegisterRuntime(server)
	}
	application, err := foundationapp.NewApp(context.Background(), spec, config, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	return application
}

func runtimeShutdownCall(t *testing.T, protocol, address string) func() error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), runtimeTestTimeout)
	t.Cleanup(cancel)
	if protocol == "grpc" {
		connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = connection.Close() })
		return func() error {
			_, err := healthpb.NewHealthClient(connection).Check(ctx, &healthpb.HealthCheckRequest{})
			return err
		}
	}
	client := &nethttp.Client{Transport: &nethttp.Transport{Proxy: nil}}
	t.Cleanup(client.CloseIdleConnections)
	return func() error {
		request, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, "http://"+address+"/inflight", nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != nethttp.StatusOK {
			return fmt.Errorf("HTTP response status %s", response.Status)
		}
		_, err = io.Copy(io.Discard, response.Body)
		return err
	}
}

func waitRuntimeShutdownSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(runtimeTestTimeout):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitRuntimeShutdownError(t *testing.T, result <-chan error, description string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(runtimeTestTimeout):
		t.Fatalf("timed out waiting for %s", description)
		return nil
	}
}

type serverManagerStub struct {
	observer    foundationconfig.Observer
	cancelCount int
}

func (*serverManagerStub) Load(string, any, ...any) error {
	return errors.New("unexpected Load call")
}

func (m *serverManagerStub) Subscribe(
	_ string,
	_ any,
	observer foundationconfig.Observer,
	_ ...any,
) (func(), error) {
	m.observer = observer
	return func() { m.cancelCount++ }, nil
}

// TestIntegrationApplicationStartupRollback 验证真实 App 在服务器 Start 之前失败时回收 HTTP 预监听。
func TestIntegrationApplicationStartupRollback(t *testing.T) {
	failure := errors.New("before-start initialization failed")
	for _, tt := range []struct {
		name           string
		callerListener bool
		grpcFailure    bool
	}{
		{name: "before-start/caller-listener", callerListener: true},
		{name: "before-start/automatic-listener"},
		{name: "later-grpc-endpoint/caller-listener", callerListener: true, grpcFailure: true},
		{name: "later-grpc-endpoint/automatic-listener", grpcFailure: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			configuration := &config_pb.Server{
				Http: &config_pb.HttpServerOption{Addr: proto.String("127.0.0.1:0")},
				Grpc: &config_pb.GrpcServerOption{Disable: proto.Bool(true)},
			}
			spec := NewSpec()
			if tt.callerListener {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				// 红测也直接释放测试持有的资源，不通过 Stop 后 Start 绕过 SDK 缺陷。
				t.Cleanup(func() { _ = listener.Close() })
				spec.HTTP().Listener(listener)
			}
			if tt.grpcFailure {
				occupied, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = occupied.Close() })
				configuration.Grpc = &config_pb.GrpcServerOption{
					Disable: proto.Bool(false), Addr: proto.String(occupied.Addr().String()),
				}
			}
			manager := newRuntimeShutdownManager(t, configuration, runtimeTestTimeout)
			logger := newRuntimeTestLogger(t)
			runtime, cleanup, err := NewRuntime(manager, logger,
				testMetricsProvider{registry: prometheus.NewRegistry()},
				runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}, spec)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			// 自动监听使用随机端口；先解析 SDK Endpoint 获取分配地址，App 随后读取同一预监听。
			endpoint, err := runtime.http.Endpoint()
			if err != nil {
				t.Fatal(err)
			}
			address := endpoint.Host
			hookCalled := false
			application := newRuntimeShutdownApp(t, manager, logger, runtime, func(context.Context) error {
				hookCalled = true
				return failure
			})
			runErr := application.Run()
			if tt.grpcFailure {
				var listenErr *net.OpError
				if !errors.As(runErr, &listenErr) || !errors.Is(runErr, syscall.EADDRINUSE) {
					t.Fatalf("Run error = %v, want original gRPC address-in-use error", runErr)
				}
				if hookCalled {
					t.Fatal("BeforeStart ran after gRPC Endpoint failure")
				}
			} else if !errors.Is(runErr, failure) || !hookCalled {
				t.Fatalf("Run error = %v, BeforeStart ran = %t; want original hook failure", runErr, hookCalled)
			}
			// Run 退出时就必须回滚；后续 cleanup 不能掩盖未释放预监听的生命周期缺陷。
			probe, err := net.Listen("tcp", address)
			if err != nil {
				t.Fatalf("HTTP listener remains bound after startup failure: %v", err)
			}
			if err := probe.Close(); err != nil {
				t.Fatal(err)
			}
			cleanup()
			cleanup()

			// 同进程创建全新 Runtime 与 App，复用失败实例的地址完成请求与正常停止。
			listener, err := net.Listen("tcp", address)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			recoveredSpec := NewSpec()
			recoveredSpec.HTTP().Listener(listener).Register(
				HandleHTTP(nethttp.MethodGet, "/inflight", func(*nethttp.Request) (any, error) {
					return map[string]string{"status": "completed"}, nil
				}))
			recoveredManager := newRuntimeShutdownManager(t, &config_pb.Server{
				Http: &config_pb.HttpServerOption{Addr: proto.String(address)},
				Grpc: &config_pb.GrpcServerOption{Disable: proto.Bool(true)},
			}, runtimeTestTimeout)
			recovered, cleanupRecovered, err := NewRuntime(recoveredManager, logger,
				testMetricsProvider{registry: prometheus.NewRegistry()},
				runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}, recoveredSpec)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanupRecovered)
			recoveredApp := newRuntimeShutdownApp(t, recoveredManager, logger, recovered)
			runResult := make(chan error, 1)
			runFinished := make(chan struct{})
			go func() {
				defer close(runFinished)
				runResult <- recoveredApp.Run()
			}()
			t.Cleanup(func() {
				if err := recoveredApp.Stop(); err != nil {
					t.Error(err)
				}
				waitRuntimeShutdownSignal(t, runFinished, "recovered application cleanup")
			})
			if err := runtimeShutdownCall(t, "http", address)(); err != nil {
				t.Fatalf("recovered application request: %v", err)
			}
			if err := recoveredApp.Stop(); err != nil {
				t.Fatal(err)
			}
			if err := waitRuntimeShutdownError(t, runResult, "recovered application Run"); err != nil {
				t.Fatal(err)
			}
			cleanupRecovered()
			cleanupRecovered()
			probe, err = net.Listen("tcp", address)
			if err != nil {
				t.Fatalf("recovered application listener remains bound after Stop: %v", err)
			}
			if err := probe.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestIntegrationApplicationStartupRollbackManagementListener(t *testing.T) {
	manager := newRuntimeShutdownManager(t, &config_pb.Server{Http: &config_pb.HttpServerOption{
		Disable: proto.Bool(true),
		Metrics: &config_pb.HttpServerOption_Metrics{Addr: proto.String("127.0.0.1:0")},
		Health:  &config_pb.HttpServerOption_Health{Addr: proto.String("127.0.0.1:0")},
	}}, runtimeTestTimeout)
	logger := newRuntimeTestLogger(t)
	runtime, cleanup, err := NewRuntime(manager, logger,
		testMetricsProvider{registry: prometheus.NewRegistry()},
		runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}, NewSpec())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if len(runtime.management) != 1 {
		t.Fatalf("management listener count = %d, want 1", len(runtime.management))
	}
	// 管理端口不暴露 Endpointer；模拟调用方已准备监听，再检验 App 失败回滚。
	endpoint, err := runtime.management[0].Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("before-start management initialization failed")
	application := newRuntimeShutdownApp(t, manager, logger, runtime, func(context.Context) error {
		return failure
	})
	if err := application.Run(); !errors.Is(err, failure) {
		t.Fatalf("Run error = %v, want original startup failure", err)
	}
	assertHTTPRuntimeAddressReleased(t, endpoint.Host)
	cleanup()
	cleanup()
}
