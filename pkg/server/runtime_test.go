package server

import (
	"context"
	"errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/protobuf/proto"
	"net/url"
	"strings"
	"testing"
	"time"
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

func TestHTTPAndGRPCRuntimeRespectProtocolEnablementAndRegisterCallbacks(t *testing.T) {
	config, err := loadConfig(testconfig.Empty(t))
	if err != nil {
		t.Fatal(err)
	}
	spec := NewSpec()
	var httpRegistered, grpcRegistered bool
	spec.HTTP().Endpoint(func(HTTPServer) error { httpRegistered = true; return nil })
	spec.GRPC().Service(func(GRPCServer) error { grpcRegistered = true; return nil })
	httpServer, err := newHTTPServer(config, newHTTPServerOptions(config, nil, spec), spec, nil, newWebSocketHub())
	if err != nil || httpServer == nil || !httpRegistered {
		t.Fatalf("http=%v registered=%t err=%v", httpServer, httpRegistered, err)
	}
	grpcServer, err := newGRPCServer(config, newGRPCServerOptions(config, nil, spec), spec)
	if err != nil || grpcServer == nil || !grpcRegistered {
		t.Fatalf("grpc=%v registered=%t err=%v", grpcServer, grpcRegistered, err)
	}
	config = proto.CloneOf(config)
	config.Http.Disable = boolp(true)
	config.Grpc.Disable = boolp(true)
	httpServer, err = newHTTPServer(config, nil, spec, nil, newWebSocketHub())
	if err != nil || httpServer != nil {
		t.Fatalf("disabled http=%v err=%v", httpServer, err)
	}
	grpcServer, err = newGRPCServer(config, nil, spec)
	if err != nil || grpcServer != nil {
		t.Fatalf("disabled grpc=%v err=%v", grpcServer, err)
	}
}

func strp(v string) *string { return &v }

func boolp(v bool) *bool { return &v }
