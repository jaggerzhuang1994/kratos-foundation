package requestdebug

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/go-kratos/kratos/v2/transport"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/request"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

type testHeader http.Header

func (h testHeader) Get(k string) string      { return http.Header(h).Get(k) }
func (h testHeader) Set(k, v string)          { http.Header(h).Set(k, v) }
func (h testHeader) Add(k, v string)          { http.Header(h).Add(k, v) }
func (h testHeader) Values(k string) []string { return http.Header(h).Values(k) }
func (h testHeader) Keys() []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	return keys
}

type testTransport struct {
	kind   transport.Kind
	header testHeader
}

func (t testTransport) Kind() transport.Kind            { return t.kind }
func (t testTransport) Endpoint() string                { return "" }
func (t testTransport) Operation() string               { return "/debug" }
func (t testTransport) RequestHeader() transport.Header { return t.header }
func (t testTransport) ReplyHeader() transport.Header   { return t.header }

func TestServerAcceptsOnlySingleDebugValue(t *testing.T) {
	if Server(nil) == nil || Server(&config_pb.Middleware_RequestDebug{}) == nil {
		t.Fatal("server must accept incoming debug by default")
	}
	if Server(&config_pb.Middleware_RequestDebug{AcceptIncoming: proto.Bool(false)}) != nil {
		t.Fatal("explicit false must disable incoming debug")
	}
	for _, kind := range []transport.Kind{transport.KindHTTP, transport.KindGRPC} {
		for _, values := range [][]string{nil, {"1"}, {""}, {"0"}, {"true"}, {" 1"}, {"1,1"}, {"1", "1"}} {
			header := testHeader{}
			for _, v := range values {
				header.Add(Header, v)
			}
			ctx := transport.NewServerContext(context.Background(), testTransport{kind: kind, header: header})
			_, err := Server(nil)(func(ctx context.Context, _ any) (any, error) {
				want := len(values) == 1 && values[0] == "1"
				if request.IsDebug(ctx) != want {
					t.Fatalf("%s values=%v debug=%v", kind, values, request.IsDebug(ctx))
				}
				return nil, nil
			})(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestClientUsesContextAndClearsForgedMetadata(t *testing.T) {
	for _, kind := range []transport.Kind{transport.KindHTTP, transport.KindGRPC} {
		for _, debug := range []bool{false, true} {
			for _, propagate := range []bool{false, true} {
				header := testHeader{}
				header.Add(Header, "1")
				header.Add(Header, "1")
				parent := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(Header, "1", "other", "kept"))
				ctx := parent
				if debug {
					ctx = request.WithDebug(ctx)
				}
				ctx = transport.NewClientContext(ctx, testTransport{kind: kind, header: header})
				_, err := Client(&config_pb.Middleware_RequestDebug{Propagate: proto.Bool(propagate)})(func(ctx context.Context, _ any) (any, error) {
					want := ""
					if debug && propagate {
						want = "1"
					}
					if got := header.Values(Header); len(got) != 1 || got[0] != want {
						t.Fatalf("header=%v want %q", got, want)
					}
					if kind == transport.KindGRPC {
						md, _ := metadata.FromOutgoingContext(ctx)
						if len(md.Get(Header)) != 0 || md.Get("other")[0] != "kept" {
							t.Fatalf("native metadata=%v", md)
						}
					}
					return nil, nil
				})(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				md, _ := metadata.FromOutgoingContext(parent)
				if len(md.Get(Header)) != 1 {
					t.Fatal("mutated parent metadata")
				}
			}
		}
	}
}

func TestDebugPropagatesAcrossIndependentContexts(t *testing.T) {
	ctx := request.WithDebug(context.Background())
	for i := 0; i < 2; i++ {
		h := testHeader{}
		outgoing := transport.NewClientContext(ctx, testTransport{kind: transport.KindHTTP, header: h})
		_, err := Client(nil)(func(context.Context, any) (any, error) { return nil, nil })(outgoing, nil)
		if err != nil {
			t.Fatal(err)
		}
		incoming := transport.NewServerContext(context.Background(), testTransport{kind: transport.KindHTTP, header: h})
		_, err = Server(&config_pb.Middleware_RequestDebug{AcceptIncoming: proto.Bool(true)})(func(derived context.Context, _ any) (any, error) { ctx = derived; return nil, nil })(incoming, nil)
		if err != nil || !request.IsDebug(ctx) {
			t.Fatalf("hop %d: debug=%v err=%v", i, request.IsDebug(ctx), err)
		}
	}
}

func TestMissingTransportPreservesLocalStateAndErrors(t *testing.T) {
	cause := errors.New("handler failed")
	for _, mw := range []func(context.Context, any) (any, error){
		Client(nil)(func(ctx context.Context, _ any) (any, error) {
			if !request.IsDebug(ctx) {
				t.Fatal("lost local state")
			}
			return nil, cause
		}),
		Server(&config_pb.Middleware_RequestDebug{AcceptIncoming: proto.Bool(true)})(func(ctx context.Context, _ any) (any, error) {
			if !request.IsDebug(ctx) {
				t.Fatal("lost local state")
			}
			return nil, cause
		}),
	} {
		if _, err := mw(request.WithDebug(context.Background()), nil); !errors.Is(err, cause) {
			t.Fatal("lost handler error")
		}
	}
}
