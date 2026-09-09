package circuitbreaker

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/transport"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type testTransport struct{}

func (testTransport) Kind() transport.Kind            { return transport.KindHTTP }
func (testTransport) Endpoint() string                { return "http://upstream" }
func (testTransport) Operation() string               { return "/orders/Get" }
func (testTransport) RequestHeader() transport.Header { return nil }
func (testTransport) ReplyHeader() transport.Header   { return nil }

func TestClientDisabledOrNilConfigReturnsNil(t *testing.T) {
	if Client(nil) != nil || Client(&config_pb.Middleware_CircuitBreaker{}) != nil {
		t.Fatal("disabled circuit breaker must not install middleware")
	}
}

func TestClientEnabledPassesSuccessAndErrorToHandler(t *testing.T) {
	mw := Client(&config_pb.Middleware_CircuitBreaker{Enable: boolp(true)})
	if mw == nil {
		t.Fatal("enabled circuit breaker returned nil")
	}
	ctx := transport.NewClientContext(context.Background(), testTransport{})
	wantErr := errors.New("upstream")
	for _, tc := range []struct {
		name string
		err  error
	}{{"success", nil}, {"error", wantErr}} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			got, err := mw(func(context.Context, any) (any, error) { called = true; return "reply", tc.err })(ctx, nil)
			if !called || got != "reply" || !errors.Is(err, tc.err) {
				t.Fatalf("called=%t reply=%v err=%v", called, got, err)
			}
		})
	}
}

func boolp(v bool) *bool { return &v }

func TestNewSREBreakerAcceptsNilAndExplicitOptions(t *testing.T) {
	if newSREBreaker(nil) == nil || newSREBreaker(&config_pb.Middleware_CircuitBreaker_SREBreaker{}) == nil {
		t.Fatal("breaker must be constructed for nil and empty config")
	}
}

func TestValidateSREDefaultsAndBoundaries(t *testing.T) {
	if err := Validate(nil); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name    string
		config  *config_pb.Middleware_CircuitBreaker_SREBreaker
		wantErr bool
	}{
		{name: "defaults"},
		{name: "one nanosecond bucket", config: &config_pb.Middleware_CircuitBreaker_SREBreaker{Window: durationpb.New(10 * time.Nanosecond)}},
		{name: "maximum duration", config: &config_pb.Middleware_CircuitBreaker_SREBreaker{Window: durationpb.New(math.MaxInt64), Bucket: proto.Int32(1)}},
		{name: "zero request and full success", config: &config_pb.Middleware_CircuitBreaker_SREBreaker{Success: proto.Float64(1), Request: proto.Int64(0)}},
		{name: "below one nanosecond", config: &config_pb.Middleware_CircuitBreaker_SREBreaker{Window: durationpb.New(9 * time.Nanosecond)}, wantErr: true},
		{name: "large bucket count small window", config: &config_pb.Middleware_CircuitBreaker_SREBreaker{Window: durationpb.New(time.Second), Bucket: proto.Int32(math.MaxInt32)}, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := &config_pb.Middleware_CircuitBreaker{Enable: proto.Bool(true), Sre: tt.config}
			if err := Validate(config); (err != nil) != tt.wantErr {
				t.Fatalf("Validate = %v, wantErr=%t", err, tt.wantErr)
			}
			if !tt.wantErr {
				breaker := newSREBreaker(config.Sre)
				if err := breaker.Allow(); err != nil {
					t.Fatalf("initial Allow = %v", err)
				}
				breaker.MarkSuccess()
				breaker.MarkFailed()
			}
		})
	}
}
