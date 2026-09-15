package requestdebug

import (
	"context"
	"net"
	"net/url"
	"testing"
	"time"

	kratosgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/request"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

type debugHealth struct {
	healthpb.UnimplementedHealthServer
}

func (debugHealth) Check(ctx context.Context, _ *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	state := healthpb.HealthCheckResponse_NOT_SERVING
	if request.IsDebug(ctx) {
		state = healthpb.HealthCheckResponse_SERVING
	}
	return &healthpb.HealthCheckResponse{Status: state}, nil
}
func (s debugHealth) Watch(_ *healthpb.HealthCheckRequest, stream healthpb.Health_WatchServer) error {
	response, err := s.Check(stream.Context(), nil)
	if err != nil {
		return err
	}
	return stream.Send(response)
}

func TestGRPCUnaryAndStreamPropagateAtCallStart(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	defer func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	}()
	enabled := &config_pb.Middleware_RequestDebug{AcceptIncoming: proto.Bool(true)}
	server := kratosgrpc.NewServer(kratosgrpc.Listener(listener), kratosgrpc.CustomHealth(), kratosgrpc.Endpoint(&url.URL{Scheme: "grpc", Host: "debug"}), kratosgrpc.Middleware(Server(enabled)), kratosgrpc.StreamInterceptor(StreamServer(Server(enabled))))
	healthpb.RegisterHealthServer(server, debugHealth{})
	done := make(chan error, 1)
	go func() { done <- server.Start(context.Background()) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Stop(ctx); err != nil {
			t.Error(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-ctx.Done():
			t.Error("server did not stop")
		}
	}()
	for _, propagate := range []bool{true, false} {
		cfg := &config_pb.Middleware_RequestDebug{Propagate: proto.Bool(propagate)}
		conn, err := kratosgrpc.DialInsecure(context.Background(), kratosgrpc.WithEndpoint("passthrough:///debug"), kratosgrpc.WithMiddleware(Client(cfg)), kratosgrpc.WithOptions(grpc.WithDisableHealthCheck(), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }), grpc.WithChainStreamInterceptor(StreamClient(cfg))))
		if err != nil {
			t.Fatal(err)
		}
		client := healthpb.NewHealthClient(conn)
		for _, debug := range []bool{false, true} {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			// 原生 outgoing metadata 中的伪造值必须被清除，也不能与合法值组成多值头。
			ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(Header, "1", "other", "kept"))
			if debug {
				ctx = request.WithDebug(ctx)
			}
			want := healthpb.HealthCheckResponse_NOT_SERVING
			if debug && propagate {
				want = healthpb.HealthCheckResponse_SERVING
			}
			response, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			if response.Status != want {
				t.Errorf("unary debug=%v propagate=%v got=%v", debug, propagate, response.Status)
			}
			stream, err := client.Watch(ctx, &healthpb.HealthCheckRequest{})
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			response, err = stream.Recv()
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			if response.Status != want {
				t.Errorf("stream debug=%v propagate=%v got=%v", debug, propagate, response.Status)
			}
		}
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	}
}

func TestClearOutgoingDebugPreservesUnrelatedContexts(t *testing.T) {
	ctx := context.Background()
	if clearOutgoingDebug(ctx) != ctx {
		t.Fatal("changed context without metadata")
	}
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("other", "kept"))
	if clearOutgoingDebug(ctx) != ctx {
		t.Fatal("changed unrelated metadata")
	}
}
