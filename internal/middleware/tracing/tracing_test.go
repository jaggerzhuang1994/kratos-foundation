package tracing

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	foundationtracing "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type testProvider struct {
	disabled bool
	provider trace.TracerProvider
}

var _ foundationtracing.Provider = testProvider{}

func (p testProvider) Disabled() bool                       { return p.disabled }
func (p testProvider) TracerProvider() trace.TracerProvider { return p.provider }
func (p testProvider) Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	return p.provider.Tracer(name, opts...)
}

type testHeader http.Header

func (h testHeader) Get(k string) string      { return http.Header(h).Get(k) }
func (h testHeader) Set(k, v string)          { http.Header(h).Set(k, v) }
func (h testHeader) Add(k, v string)          { http.Header(h).Add(k, v) }
func (h testHeader) Keys() []string           { return nil }
func (h testHeader) Values(k string) []string { return http.Header(h).Values(k) }

type testTransport struct{}

func (testTransport) Kind() transport.Kind            { return transport.KindHTTP }
func (testTransport) Endpoint() string                { return "" }
func (testTransport) Operation() string               { return "/svc/method" }
func (testTransport) RequestHeader() transport.Header { return testHeader{} }
func (testTransport) ReplyHeader() transport.Header   { return testHeader{} }

func TestServerAndClientRespectDisableFlags(t *testing.T) {
	p := testProvider{provider: tracesdk.NewTracerProvider()}
	if Server(p, nil) == nil || Client(p, nil) == nil {
		t.Fatal("enabled tracing middleware is nil")
	}
	if Server(p, &config_pb.Middleware_Tracing{Disable: boolp(true)}) != nil || Client(testProvider{disabled: true, provider: p.provider}, nil) != nil {
		t.Fatal("disabled tracing middleware must be nil")
	}
}

func TestServerAndClientCreateSpansAndReturnHandlerError(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(recorder))
	p := testProvider{provider: provider}
	for name, mw := range map[string]func(foundationtracing.Provider, Config) middleware.Middleware{
		"server": Server, "client": Client,
	} {
		t.Run(name, func(t *testing.T) {
			want := context.Canceled
			ctx := context.Background()
			if name == "server" {
				ctx = transport.NewServerContext(ctx, testTransport{})
			} else {
				ctx = transport.NewClientContext(ctx, testTransport{})
			}
			_, err := mw(p, nil)(func(context.Context, any) (any, error) { return nil, want })(ctx, nil)
			if err != want {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if len(recorder.Ended()) != 2 {
		t.Fatalf("ended spans=%d, want 2", len(recorder.Ended()))
	}
}

func boolp(v bool) *bool { return &v }
