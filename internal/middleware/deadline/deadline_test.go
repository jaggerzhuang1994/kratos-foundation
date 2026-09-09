package deadline

import (
	"context"
	"errors"
	"net/textproto"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-kratos/kratos/v2/transport"
	deadlinecontext "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/deadline"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestClientExplicitZeroFallbackDoesNotCreateDeadline(t *testing.T) {
	policy, err := deadlinecontext.NewStore(&config_pb.Middleware_Deadline{FallbackTimeout: durationpb.New(0)})
	if err != nil {
		t.Fatal(err)
	}
	header := testHeader{}
	ctx := transport.NewClientContext(context.Background(), &testTransport{
		kind:    transport.KindHTTP,
		headers: header,
	})
	called := false
	_, err = Client(policy)(func(ctx context.Context, _ any) (any, error) {
		called = true
		if _, ok := ctx.Deadline(); ok {
			t.Fatal("zero policy created a context deadline")
		}
		if info, ok := deadlinecontext.InfoFromContext(ctx); ok {
			t.Fatalf("unlimited request recorded deadline info %+v", info)
		}
		return nil, nil
	})(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("client handler was not called")
	}
	if got := header.Get(HTTPTimeoutHeader); got != "" {
		t.Fatalf("unlimited policy propagated timeout header %q", got)
	}
}

func TestMiddlewareDefaultsToTenSeconds(t *testing.T) {
	for _, side := range []string{"client", "server"} {
		t.Run(side, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				store, err := deadlinecontext.NewStore(nil)
				if err != nil {
					t.Fatal(err)
				}
				header := testHeader{}
				tr := &testTransport{kind: transport.KindHTTP, headers: header}
				ctx := transport.NewServerContext(context.Background(), tr)
				middleware := Server(store)
				if side == "client" {
					ctx = transport.NewClientContext(context.Background(), tr)
					middleware = Client(store)
				}
				_, err = middleware(func(ctx context.Context, _ any) (any, error) {
					info, ok := deadlinecontext.InfoFromContext(ctx)
					if !ok || info.Source != deadlinecontext.SourceFallback || info.RemainingAtApply != 10*time.Second {
						t.Fatalf("default deadline info = %+v, present=%v", info, ok)
					}
					if side == "client" && header.Get(HTTPTimeoutHeader) != "10000" {
						t.Fatalf("default timeout header = %q", header.Get(HTTPTimeoutHeader))
					}
					<-ctx.Done()
					if elapsed := time.Since(info.AppliedAt); elapsed != 10*time.Second {
						t.Fatalf("timeout after %s, want 10s", elapsed)
					}
					return nil, ctx.Err()
				})(ctx, nil)
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("error = %v, want deadline exceeded", err)
				}
			})
		})
	}
}

func TestClientPropagatesFallbackBudget(t *testing.T) {
	policy, err := deadlinecontext.NewStore(&config_pb.Middleware_Deadline{
		FallbackTimeout: durationpb.New(5 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	header := testHeader{}
	ctx := transport.NewClientContext(context.Background(), &testTransport{
		kind:    transport.KindHTTP,
		headers: header,
	})
	_, err = Client(policy)(func(context.Context, any) (any, error) {
		propagated, err := strconv.Atoi(header.Get(HTTPTimeoutHeader))
		if err != nil {
			t.Fatal(err)
		}
		if propagated < 4000 || propagated > 5000 {
			t.Fatalf("propagated timeout = %dms, want about 5s", propagated)
		}
		return nil, nil
	})(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestServerReadsHTTPBudgetByDefault(t *testing.T) {
	policy, err := deadlinecontext.NewStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	header := testHeader{}
	header.Set(HTTPTimeoutHeader, "200")
	ctx := transport.NewServerContext(context.Background(), &testTransport{
		kind:    transport.KindHTTP,
		headers: header,
	})
	_, err = Server(policy)(func(ctx context.Context, _ any) (any, error) {
		info, ok := deadlinecontext.InfoFromContext(ctx)
		if !ok || info.Source != deadlinecontext.SourceParent {
			t.Fatalf("deadline info = %+v, present=%v", info, ok)
		}
		return nil, nil
	})(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestClientRejectsBudgetBelowMinimum(t *testing.T) {
	policy, err := deadlinecontext.NewStore(&config_pb.Middleware_Deadline{
		MinBudget: durationpb.New(20 * time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	called := false
	_, err = Client(policy)(func(context.Context, any) (any, error) {
		called = true
		return nil, nil
	})(ctx, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
	if called {
		t.Fatal("client handler was called below min budget")
	}
}

func TestServerRejectsIncomingBudgetBelowMinimum(t *testing.T) {
	store, err := deadlinecontext.NewStore(&config_pb.Middleware_Deadline{
		MinBudget: durationpb.New(20 * time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	header := testHeader{}
	header.Set(HTTPTimeoutHeader, "10")
	ctx := transport.NewServerContext(context.Background(), &testTransport{
		kind:    transport.KindHTTP,
		headers: header,
	})
	called := false

	_, err = Server(store)(func(context.Context, any) (any, error) {
		called = true
		return nil, nil
	})(ctx, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
	if called {
		t.Fatal("server handler was called below min budget")
	}
}

func TestClientConvertsContextDeadlineForGRPC(t *testing.T) {
	store, err := deadlinecontext.NewStore(&config_pb.Middleware_Deadline{
		FallbackTimeout: durationpb.New(5 * time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := transport.NewClientContext(context.Background(), &testTransport{
		kind:    transport.KindGRPC,
		headers: testHeader{},
	})

	response, err := Client(store)(func(ctx context.Context, _ any) (any, error) {
		<-ctx.Done()
		return "partial", errors.New("transport error")
	})(ctx, nil)
	if response != "partial" {
		t.Fatalf("response = %#v", response)
	}
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("status code = %s, error = %v", status.Code(err), err)
	}
}

type testTransport struct {
	kind    transport.Kind
	headers testHeader
}

func (t *testTransport) Kind() transport.Kind            { return t.kind }
func (t *testTransport) Endpoint() string                { return "" }
func (t *testTransport) Operation() string               { return "/deadline.Test/Call" }
func (t *testTransport) RequestHeader() transport.Header { return t.headers }
func (t *testTransport) ReplyHeader() transport.Header   { return testHeader{} }

type testHeader map[string][]string

func (h testHeader) Get(key string) string {
	values := h[textproto.CanonicalMIMEHeaderKey(key)]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (h testHeader) Set(key, value string) {
	h[textproto.CanonicalMIMEHeaderKey(key)] = []string{value}
}

func (h testHeader) Add(key, value string) {
	key = textproto.CanonicalMIMEHeaderKey(key)
	h[key] = append(h[key], value)
}

func (h testHeader) Keys() []string {
	keys := make([]string, 0, len(h))
	for key := range h {
		keys = append(keys, key)
	}
	return keys
}

func (h testHeader) Values(key string) []string {
	return append([]string(nil), h[textproto.CanonicalMIMEHeaderKey(key)]...)
}
