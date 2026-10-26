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

	"github.com/go-kratos/kratos/v2/transport"
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
	"google.golang.org/protobuf/types/known/durationpb"
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

// 真实 Kratos gRPC 客户端默认启用客户端健康检查（Watch 空服务名）：未就绪时不发流量，就绪后恢复，
// 停机延迟期间先摘流，且活跃的 Watch 流不阻塞 GracefulStop。
func TestIntegrationKratosClientHealthCheckFollowsLifecycle(t *testing.T) {
	const stopDelay = 2 * time.Second
	runtime, cleanup, err := NewRuntime(testconfig.New(t, "server", &config_pb.Server{
		Http:      &config_pb.HttpServerOption{Disable: boolp(true)},
		Grpc:      &config_pb.GrpcServerOption{Disable: boolp(false), Addr: strp("127.0.0.1:0")},
		StopDelay: durationpb.New(stopDelay),
	}), newRuntimeTestLogger(t), testMetricsProvider{registry: prometheus.NewRegistry()},
		runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}, NewSpec())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	var ready atomic.Bool
	runtime.SetReadinessSource(ready.Load)
	_, grpcServer := runtime.Servers()
	// Kratos 的 Endpoint 与 Start 都会惰性监听，先解析端点再启动，避免并发初始化同一 listener。
	endpoint, err := grpcServer.(transport.Endpointer).Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- grpcServer.Start(context.Background()) }()
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			ctx, cancel := context.WithTimeout(context.Background(), runtimeTestTimeout)
			defer cancel()
			_ = grpcServer.Stop(ctx)
			<-finished
		}
	})
	connection, err := kratosgrpc.DialInsecure(t.Context(), kratosgrpc.WithEndpoint(endpoint.Host))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := healthpb.NewHealthClient(connection)
	// liveness 恒 SERVING，调用成功与否只取决于客户端是否把该实例视为可用。
	call := func() error {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		_, err := client.Check(ctx, &healthpb.HealthCheckRequest{Service: grpcLivenessService})
		return err
	}
	held := func(err error) bool {
		code := status.Code(err)
		return code == codes.Unavailable || code == codes.DeadlineExceeded
	}
	waitFor := func(what string, done func(error) bool, within time.Duration) {
		t.Helper()
		deadline := time.Now().Add(within)
		for {
			err := call()
			if done(err) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: last call error = %v", what, err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	// 端口已监听但应用未就绪：客户端持续拒绝把请求发往该实例。
	time.Sleep(3 * grpcHealthWatchInterval / 2)
	for range 3 {
		if err := call(); !held(err) {
			t.Fatalf("call before ready = %v, want held", err)
		}
	}
	ready.Store(true)
	waitFor("traffic after ready", func(err error) bool { return err == nil }, 2*grpcHealthWatchInterval+time.Second)

	// 应用进入停机：就绪先撤销，客户端在停机延迟结束、监听关闭之前摘流。
	ready.Store(false)
	stopStarted := time.Now()
	stopResult := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		stopResult <- grpcServer.Stop(ctx)
	}()
	waitFor("drain during stop delay", held, 2*grpcHealthWatchInterval)
	if elapsed := time.Since(stopStarted); elapsed >= stopDelay {
		t.Fatalf("client drained after %v, not within stop delay %v", elapsed, stopDelay)
	}
	// 客户端连接与 Watch 流仍然存在；GracefulStop 须远早于 10s 强制停止期限完成。
	select {
	case err := <-stopResult:
		stopped = true
		if err != nil {
			t.Fatalf("Stop = %v", err)
		}
		if elapsed := time.Since(stopStarted); elapsed > stopDelay+2*time.Second {
			t.Fatalf("Stop took %v; Watch stream blocked GracefulStop", elapsed)
		}
	case <-time.After(stopDelay + 5*time.Second):
		t.Fatal("Stop blocked by active health Watch stream")
	}
	if err := <-finished; err != nil {
		t.Fatalf("Start = %v", err)
	}
}
