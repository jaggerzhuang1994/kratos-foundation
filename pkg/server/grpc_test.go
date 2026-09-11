package server

import (
	"context"
	"net"
	"net/url"
	"testing"

	kratosgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/prometheus/client_golang/prometheus"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// TestIntegrationGRPCServices 使用内存连接运行真实 gRPC 编解码和服务分发，不依赖外部服务。
func TestIntegrationGRPCServices(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	service := health.NewServer()
	service.SetServingStatus("orders", healthpb.HealthCheckResponse_SERVING)
	service.SetServingStatus("maintenance", healthpb.HealthCheckResponse_NOT_SERVING)
	spec := NewSpec()
	spec.HTTP().Disable()
	// 自定义健康服务须关闭 Kratos 自动注册，避免重复服务名。
	// 内存 listener 没有 TCP 端口，显式 endpoint 避免运行时尝试推导地址。
	spec.GRPC().Option(kratosgrpc.Listener(listener), kratosgrpc.CustomHealth(),
		kratosgrpc.Endpoint(&url.URL{Scheme: "grpc", Host: "integration"})).Register(func(srv GRPCServer) error {
		healthpb.RegisterHealthServer(srv, service)
		return nil
	})
	runtime, cleanup, err := NewRuntime(testconfig.Empty(t), newRuntimeTestLogger(t),
		testMetricsProvider{registry: prometheus.NewRegistry()},
		runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	httpServer, grpcServer := runtime.Servers()
	if httpServer != nil || grpcServer == nil {
		t.Fatalf("protocol selection: HTTP=%v gRPC=%v", httpServer, grpcServer)
	}
	// Start 由当前测试持有；cleanup 先停止监听再等待退出，失败路径同样回收 goroutine。
	finished := make(chan error, 1)
	go func() { finished <- grpcServer.Start(t.Context()) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), runtimeTestTimeout)
		defer cancel()
		if err := grpcServer.Stop(ctx); err != nil {
			t.Error(err)
		}
		select {
		case err := <-finished:
			if err != nil {
				t.Errorf("gRPC Start: %v", err)
			}
		case <-ctx.Done():
			t.Error("gRPC Start did not exit after Stop")
		}
	})
	connection, err := grpc.NewClient("passthrough:///integration",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})
	client := healthpb.NewHealthClient(connection)
	for _, test := range []struct {
		name, service string
		canceled      bool
		code          codes.Code
		status        healthpb.HealthCheckResponse_ServingStatus
	}{
		{"registered service", "orders", false, codes.OK, healthpb.HealthCheckResponse_SERVING},
		{"service not ready", "maintenance", false, codes.OK, healthpb.HealthCheckResponse_NOT_SERVING},
		{"unknown service", "missing", false, codes.NotFound, healthpb.HealthCheckResponse_UNKNOWN},
		{"caller canceled", "orders", true, codes.Canceled, healthpb.HealthCheckResponse_UNKNOWN},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), runtimeTestTimeout)
			defer cancel()
			if test.canceled {
				cancel()
			}
			response, err := client.Check(ctx, &healthpb.HealthCheckRequest{Service: test.service})
			if status.Code(err) != test.code {
				t.Fatalf("Check error=%v want code=%v", err, test.code)
			}
			if test.code == codes.OK && response.GetStatus() != test.status {
				t.Fatalf("health status=%v want=%v", response.GetStatus(), test.status)
			}
		})
	}
}
