package client

import (
	"context"
	"sync"
	"testing"

	"github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/grpc"
)

func TestCallOptionsAreIsolatedAcrossContexts(t *testing.T) {
	base := context.Background()
	if len(GRPCCallOptionsFromContext(base)) != 0 || len(HTTPCallOptionsFromContext(base)) != 0 {
		t.Fatal("unexpected defaults")
	}
	grpcOptions := []grpc.CallOption{grpc.WaitForReady(true)}
	httpOptions := []http.CallOption{http.Operation("base")}
	parent := WithGRPCCallOptions(WithHTTPCallOptions(base, httpOptions...), grpcOptions...)
	grpcOptions[0], httpOptions[0] = nil, nil
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			child := WithGRPCCallOptions(WithHTTPCallOptions(parent, http.Operation("child")), grpc.MaxCallRecvMsgSize(1024))
			g, h := GRPCCallOptionsFromContext(child), HTTPCallOptionsFromContext(child)
			if len(g) != 2 || len(h) != 2 || g[0] == nil || h[0] == nil {
				t.Error("lost or aliased options")
				return
			}
			g[0], h[0] = nil, nil
			if GRPCCallOptionsFromContext(child)[0] == nil || HTTPCallOptionsFromContext(child)[0] == nil {
				t.Error("getter exposed backing array")
			}
		})
	}
	wg.Wait()
	if len(GRPCCallOptionsFromContext(parent)) != 1 || len(HTTPCallOptionsFromContext(parent)) != 1 {
		t.Fatal("parent modified")
	}
}
