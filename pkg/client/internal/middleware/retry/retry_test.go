package retry

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type fakeTransport struct {
	kind      transport.Kind
	operation string
	request   *http.Request
}

func (t fakeTransport) Kind() transport.Kind            { return t.kind }
func (t fakeTransport) Endpoint() string                { return "" }
func (t fakeTransport) Operation() string               { return t.operation }
func (t fakeTransport) RequestHeader() transport.Header { return nil }
func (t fakeTransport) ReplyHeader() transport.Header   { return nil }
func (t fakeTransport) Request() *http.Request          { return t.request }

func httpContext(method string) context.Context {
	request, _ := http.NewRequest(method, "http://orders/v1/orders", nil)
	return transport.NewClientContext(context.Background(), fakeTransport{kind: transport.KindHTTP, operation: "/orders.v1.Orders/Get", request: request})
}

func grpcContext(operation string) context.Context {
	return transport.NewClientContext(context.Background(), fakeTransport{kind: transport.KindGRPC, operation: operation})
}

var unavailable = kratoserrors.ServiceUnavailable("UNAVAILABLE", "node down")

// failing 前 failures 次返回 err，之后成功，并记录调用时间。
func failing(failures int, err error, calls *[]time.Time) func(context.Context, any) (any, error) {
	return func(context.Context, any) (any, error) {
		*calls = append(*calls, time.Now())
		if len(*calls) <= failures {
			return nil, err
		}
		return "ok", nil
	}
}

func TestRetryReplaysOnlySafeCalls(t *testing.T) {
	config := &config_pb.Middleware_Retry{Enable: proto.Bool(true), Operations: []string{"/orders.v1.Orders/Get"}}
	for _, tc := range []struct {
		name  string
		ctx   context.Context
		err   error
		calls int
	}{
		{"http get", httpContext(http.MethodGet), unavailable, 3},
		{"http post has a consumed body", httpContext(http.MethodPost), unavailable, 1},
		{"grpc listed operation", grpcContext("/orders.v1.Orders/Get"), unavailable, 3},
		{"grpc unlisted operation", grpcContext("/orders.v1.Orders/Create"), unavailable, 1},
		{"business error", grpcContext("/orders.v1.Orders/Get"), kratoserrors.NotFound("ORDER_NOT_FOUND", "missing"), 1},
		{"open circuit breaker", grpcContext("/orders.v1.Orders/Get"), kratoserrors.ServiceUnavailable(circuitBreakerReason, "open"), 1},
		{"timeout", grpcContext("/orders.v1.Orders/Get"), context.DeadlineExceeded, 1},
		{"no transport", context.Background(), unavailable, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var calls []time.Time
				_, err := Client(config)(failing(10, tc.err, &calls))(tc.ctx, nil)
				if len(calls) != tc.calls || !errors.Is(err, tc.err) && !kratoserrors.Is(err, tc.err) {
					t.Fatalf("calls=%d err=%v, want %d calls", len(calls), err, tc.calls)
				}
			})
		})
	}
}

// 退避倍增并带抖动，不超过 max_backoff；成功即停止。
func TestRetryBackoffAndRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		config := &config_pb.Middleware_Retry{Enable: proto.Bool(true), MaxAttempts: proto.Int32(5), Backoff: durationpb.New(100 * time.Millisecond), MaxBackoff: durationpb.New(250 * time.Millisecond)}
		var calls []time.Time
		reply, err := Client(config)(failing(4, unavailable, &calls))(httpContext(http.MethodGet), nil)
		if err != nil || reply != "ok" || len(calls) != 5 {
			t.Fatalf("reply=%v err=%v calls=%d", reply, err, len(calls))
		}
		for i, ceiling := range []time.Duration{100, 200, 250, 250} {
			gap := calls[i+1].Sub(calls[i])
			if gap < ceiling*time.Millisecond/2 || gap > ceiling*time.Millisecond {
				t.Fatalf("retry %d waited %v, want within [%v, %v]", i+1, gap, ceiling*time.Millisecond/2, ceiling*time.Millisecond)
			}
		}
	})
}

// 剩余截止时间不足以等待退避时直接返回最后一次错误，不浪费预算。
func TestRetryRespectsDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		config := &config_pb.Middleware_Retry{Enable: proto.Bool(true), Backoff: durationpb.New(time.Second), MaxBackoff: durationpb.New(time.Second)}
		ctx, cancel := context.WithTimeout(httpContext(http.MethodGet), 200*time.Millisecond)
		defer cancel()
		var calls []time.Time
		if _, err := Client(config)(failing(10, unavailable, &calls))(ctx, nil); !kratoserrors.Is(err, unavailable) || len(calls) != 1 {
			t.Fatalf("calls=%d err=%v", len(calls), err)
		}
	})
}

func TestRetryValidateAndDisabled(t *testing.T) {
	if Client(nil) != nil || Client(&config_pb.Middleware_Retry{}) != nil {
		t.Fatal("disabled retry built a middleware")
	}
	for config, want := range map[*config_pb.Middleware_Retry]string{
		{Enable: proto.Bool(true), Backoff: durationpb.New(2 * time.Second)}: "exceeds max_backoff",
		{Enable: proto.Bool(true), Operations: []string{""}}:                 "empty strings",
	} {
		if err := Validate(config); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("validate %v = %v, want %q", config, err, want)
		}
	}
	if err := Validate(&config_pb.Middleware_Retry{Backoff: durationpb.New(time.Hour)}); err != nil {
		t.Fatalf("disabled config validated: %v", err)
	}
}
