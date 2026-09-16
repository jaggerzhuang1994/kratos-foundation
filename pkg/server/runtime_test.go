package server

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware"
	kratosgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

type testServer struct {
	stops   int
	stopErr error
}

func (s *testServer) Start(context.Context) error { return nil }

func (s *testServer) Stop(context.Context) error { s.stops++; return s.stopErr }

func (s *testServer) Endpoint() (*url.URL, error) { return &url.URL{Scheme: "http", Host: "test"}, nil }

type testMetricsProvider struct{ registry *prometheus.Registry }

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
