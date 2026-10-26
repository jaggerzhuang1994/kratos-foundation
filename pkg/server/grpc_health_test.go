package server

import (
	"context"
	"errors"
	"net"
	"net/url"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	kratosgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func newTestGRPCHealth(ready *atomic.Bool, check func(context.Context) error) *grpcHealth {
	config := healthConfig{}
	if check != nil {
		config.Checks = []ReadinessCheck{{Name: "database", Check: check}}
	}
	state := newHealthState(config)
	state.applicationReady = ready.Load
	return &grpcHealth{state: state, services: func() map[string]grpc.ServiceInfo {
		return map[string]grpc.ServiceInfo{"orders.v1.Orders": {}}
	}}
}

// Check 与 HTTP readiness 一致：应用就绪、未停机且依赖检查通过才 SERVING；liveness 恒 SERVING；未知服务 NotFound。
func TestGRPCHealthCheckFollowsReadiness(t *testing.T) {
	var ready atomic.Bool
	var failing atomic.Bool
	health := newTestGRPCHealth(&ready, func(context.Context) error {
		if failing.Load() {
			return errors.New("database down")
		}
		return nil
	})
	check := func(service string) healthpb.HealthCheckResponse_ServingStatus {
		t.Helper()
		response, err := health.Check(context.Background(), &healthpb.HealthCheckRequest{Service: service})
		if err != nil {
			t.Fatalf("Check(%q) = %v", service, err)
		}
		return response.GetStatus()
	}
	if check("") != healthpb.HealthCheckResponse_NOT_SERVING || check(grpcLivenessService) != healthpb.HealthCheckResponse_SERVING {
		t.Fatal("not ready application must be NOT_SERVING while liveness stays SERVING")
	}
	ready.Store(true)
	if check("") != healthpb.HealthCheckResponse_SERVING || check("orders.v1.Orders") != healthpb.HealthCheckResponse_SERVING {
		t.Fatal("ready application must be SERVING for the server and registered services")
	}
	failing.Store(true)
	if check("") != healthpb.HealthCheckResponse_NOT_SERVING || check(grpcLivenessService) != healthpb.HealthCheckResponse_SERVING {
		t.Fatal("failing dependency must turn readiness NOT_SERVING only")
	}
	failing.Store(false)
	health.state.stopped.Store(true)
	if check("") != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Fatal("stopping server must be NOT_SERVING")
	}
	if _, err := health.Check(context.Background(), &healthpb.HealthCheckRequest{Service: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown service = %v, want NotFound", err)
	}
}

type healthWatchStream struct {
	grpc.ServerStream
	ctx  context.Context
	sent chan healthpb.HealthCheckResponse_ServingStatus
}

func (s *healthWatchStream) Context() context.Context { return s.ctx }
func (s *healthWatchStream) Send(response *healthpb.HealthCheckResponse) error {
	s.sent <- response.GetStatus()
	return nil
}

// Watch 立即推送当前状态，只在变化时再推送，客户端断开后返回；不执行依赖检查。
func TestGRPCHealthWatchPushesChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var ready atomic.Bool
		checks := 0
		health := newTestGRPCHealth(&ready, func(context.Context) error { checks++; return nil })
		ctx, cancel := context.WithCancel(context.Background())
		stream := &healthWatchStream{ctx: ctx, sent: make(chan healthpb.HealthCheckResponse_ServingStatus, 8)}
		done := make(chan error, 1)
		go func() { done <- health.Watch(&healthpb.HealthCheckRequest{}, stream) }()
		synctest.Wait()
		if got := <-stream.sent; got != healthpb.HealthCheckResponse_NOT_SERVING {
			t.Fatalf("initial status = %v", got)
		}
		time.Sleep(3 * grpcHealthWatchInterval)
		synctest.Wait()
		if len(stream.sent) != 0 {
			t.Fatal("unchanged status was pushed again")
		}
		ready.Store(true)
		time.Sleep(grpcHealthWatchInterval)
		synctest.Wait()
		if got := <-stream.sent; got != healthpb.HealthCheckResponse_SERVING {
			t.Fatalf("status after ready = %v", got)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("Watch = %v", err)
		}
		if checks != 0 {
			t.Fatalf("Watch ran %d dependency checks", checks)
		}
		if err := health.Watch(&healthpb.HealthCheckRequest{Service: "missing"}, stream); status.Code(err) != codes.NotFound {
			t.Fatalf("unknown service watch = %v", err)
		}
	})
}

// 经 NewRuntime 组装的 gRPC 服务器默认注册该健康服务，真实客户端看到的状态跟随应用就绪。
func TestIntegrationGRPCHealthFollowsApplicationReadiness(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	t.Cleanup(func() { _ = listener.Close() })
	spec := NewSpec()
	spec.GRPC().Option(kratosgrpc.Listener(listener), kratosgrpc.Endpoint(&url.URL{Scheme: "grpc", Host: "health"}))
	runtime, cleanup, err := NewRuntime(testconfig.New(t, "server", &config_pb.Server{Http: &config_pb.HttpServerOption{Disable: boolp(true)}, Grpc: &config_pb.GrpcServerOption{Disable: boolp(false)}}), newRuntimeTestLogger(t),
		testMetricsProvider{registry: prometheus.NewRegistry()}, runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	var ready atomic.Bool
	runtime.SetReadinessSource(ready.Load)
	_, grpcServer := runtime.Servers()
	finished := make(chan error, 1)
	go func() { finished <- grpcServer.Start(t.Context()) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), runtimeTestTimeout)
		defer cancel()
		_ = grpcServer.Stop(ctx)
		<-finished
	})
	connection, err := grpc.NewClient("passthrough:///health", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := healthpb.NewHealthClient(connection)
	for _, want := range []healthpb.HealthCheckResponse_ServingStatus{healthpb.HealthCheckResponse_NOT_SERVING, healthpb.HealthCheckResponse_SERVING} {
		if want == healthpb.HealthCheckResponse_SERVING {
			ready.Store(true)
		}
		ctx, cancel := context.WithTimeout(t.Context(), runtimeTestTimeout)
		response, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
		cancel()
		if err != nil || response.GetStatus() != want {
			t.Fatalf("Check = %v, %v; want %v", response.GetStatus(), err, want)
		}
	}
}
