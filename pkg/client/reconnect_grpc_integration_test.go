package client

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestGRPCClientSurvivesServerRestart(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	start := func(listener net.Listener) *grpc.Server {
		server := grpc.NewServer()
		healthpb.RegisterHealthServer(server, health.NewServer())
		go func() { _ = server.Serve(listener) }()
		t.Cleanup(server.Stop)
		return server
	}
	first := start(listener)
	result, err := newTestRealBuilder(t, nil).build(context.Background(), newClientSpec("health", &config_pb.ClientOption{
		Target: "passthrough:///" + address,
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.close() })
	client := healthpb.NewHealthClient(result.grpcClient)
	check := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		response, err := client.Check(ctx, &healthpb.HealthCheckRequest{}, grpc.WaitForReady(true))
		if err != nil {
			t.Fatal(err)
		}
		if response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			t.Fatalf("health = %v", response)
		}
	}
	check()
	first.Stop()
	offlineCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, err = client.Check(offlineCtx, &healthpb.HealthCheckRequest{})
	cancel()
	if err == nil {
		t.Fatal("RPC unexpectedly succeeded while server was stopped")
	}
	listener, err = net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	start(listener)
	// 同一个已借出的 ClientConn 恢复；调用方不重建 Factory 或业务客户端。
	check()
}
