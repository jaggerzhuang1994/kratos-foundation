package client

import (
	"context"
	"errors"
	"testing"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	stdgrpc "google.golang.org/grpc"
)

type stubFactory struct {
	http     *kratoshttp.Client
	grpc     *stdgrpc.ClientConn
	err      error
	released int
}

func (f *stubFactory) AcquireClient(context.Context, string) (*kratoshttp.Client, *stdgrpc.ClientConn, func(), error) {
	if f.err != nil {
		return nil, nil, nil, f.err
	}
	return f.http, f.grpc, func() { f.released++ }, nil
}

// 类型化租用只返回请求的协议；协议不符时释放租约并返回 ErrProtocolMismatch。
func TestAcquireByProtocol(t *testing.T) {
	ctx := context.Background()
	httpFactory := &stubFactory{http: &kratoshttp.Client{}}
	if c, release, err := AcquireHTTP(ctx, httpFactory, "orders"); err != nil || c == nil {
		t.Fatalf("AcquireHTTP = %v %v", c, err)
	} else {
		release()
	}
	if _, _, err := AcquireGRPC(ctx, httpFactory, "orders"); !errors.Is(err, ErrProtocolMismatch) || httpFactory.released != 2 {
		t.Fatalf("AcquireGRPC on HTTP = %v released=%d", err, httpFactory.released)
	}
	grpcFactory := &stubFactory{grpc: &stdgrpc.ClientConn{}}
	if conn, release, err := AcquireGRPC(ctx, grpcFactory, "users"); err != nil || conn == nil {
		t.Fatalf("AcquireGRPC = %v %v", conn, err)
	} else {
		release()
	}
	if _, _, err := AcquireHTTP(ctx, grpcFactory, "users"); !errors.Is(err, ErrProtocolMismatch) || grpcFactory.released != 2 {
		t.Fatalf("AcquireHTTP on gRPC = %v released=%d", err, grpcFactory.released)
	}
	failure := errors.New("closed")
	if _, _, err := AcquireHTTP(ctx, &stubFactory{err: failure}, "x"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, _, err := AcquireGRPC(ctx, &stubFactory{err: failure}, "x"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
