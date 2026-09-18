package tracing

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-kratos/kratos/v2/middleware"
	kratostracing "github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/go-kratos/kratos/v2/transport"
	foundationtracing "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"go.opentelemetry.io/otel/propagation"
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

type testTransport struct {
	header testHeader
}

func (testTransport) Kind() transport.Kind { return transport.KindHTTP }
func (testTransport) Endpoint() string     { return "" }
func (testTransport) Operation() string    { return "/svc/method" }
func (t testTransport) RequestHeader() transport.Header {
	if t.header == nil {
		return testHeader{}
	}
	return t.header
}
func (testTransport) ReplyHeader() transport.Header { return testHeader{} }

func TestTraceContextWireContractRemainsCompatibleWithV1(t *testing.T) {
	const (
		traceID     = "0af7651916cd43dd8448eb211c80319c"
		parentSpan  = "b7ad6b7169203331"
		traceparent = "00-" + traceID + "-" + parentSpan + "-01"
	)
	provider := tracesdk.NewTracerProvider(tracesdk.WithSampler(tracesdk.AlwaysSample()))
	p := testProvider{provider: provider}

	t.Run("v1 caller to v2 server", func(t *testing.T) {
		header := testHeader{}
		header.Set("traceparent", traceparent)
		ctx := transport.NewServerContext(context.Background(), testTransport{header: header})
		_, err := Server(p, nil)(func(ctx context.Context, _ any) (any, error) {
			got := trace.SpanContextFromContext(ctx)
			if !got.IsValid() || got.TraceID().String() != traceID || got.SpanID().String() == parentSpan {
				t.Fatalf("server span context = %s/%s", got.TraceID(), got.SpanID())
			}
			return nil, nil
		})(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("v2 caller to v1 server", func(t *testing.T) {
		header := testHeader{}
		parentHeader := testHeader{}
		parentHeader.Set("traceparent", traceparent)
		parent := propagation.TraceContext{}.Extract(context.Background(), parentHeader)
		ctx := transport.NewClientContext(parent, testTransport{header: header})
		_, err := Client(p, nil)(func(context.Context, any) (any, error) { return nil, nil })(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		legacyServerContext := propagation.TraceContext{}.Extract(context.Background(), header)
		got := trace.SpanContextFromContext(legacyServerContext)
		if !got.IsValid() || got.TraceID().String() != traceID || got.SpanID().String() == parentSpan {
			t.Fatalf("legacy server span context = %s/%s", got.TraceID(), got.SpanID())
		}
	})
}

func TestServerAndClientKeepCorrelationIDsWhenTracingIsDisabled(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(recorder))
	tests := []struct {
		name     string
		build    func(foundationtracing.Provider, Config) middleware.Middleware
		provider testProvider
		config   Config
		ctx      context.Context
	}{
		{
			name:     "server middleware disabled",
			build:    Server,
			provider: testProvider{provider: provider},
			config:   &config_pb.Middleware_Tracing{Disable: boolp(true)},
			ctx:      transport.NewServerContext(context.Background(), testTransport{}),
		},
		{
			name:     "client provider disabled",
			build:    Client,
			provider: testProvider{disabled: true, provider: provider},
			ctx:      transport.NewClientContext(context.Background(), testTransport{}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mw := test.build(test.provider, test.config)
			if mw == nil {
				t.Fatal("disabled tracing middleware = nil, want correlation middleware")
			}
			_, err := mw(func(ctx context.Context, _ any) (any, error) {
				spanContext := trace.SpanContextFromContext(ctx)
				if !spanContext.TraceID().IsValid() || !spanContext.SpanID().IsValid() {
					t.Fatalf("span context = %s/%s, want valid ids", spanContext.TraceID(), spanContext.SpanID())
				}
				if got := kratostracing.TraceID()(ctx); got != spanContext.TraceID().String() {
					t.Fatalf("log trace.id = %v, want %s", got, spanContext.TraceID())
				}
				if got := kratostracing.SpanID()(ctx); got != spanContext.SpanID().String() {
					t.Fatalf("log span.id = %v, want %s", got, spanContext.SpanID())
				}
				if trace.SpanFromContext(ctx).IsRecording() {
					t.Fatal("disabled tracing created a recording span")
				}
				return nil, nil
			})(test.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
	if spans := recorder.Ended(); len(spans) != 0 {
		t.Fatalf("recorded spans = %d, want 0", len(spans))
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
