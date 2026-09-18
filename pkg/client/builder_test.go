package client

import (
	"context"
	"errors"
	"io"
	"net"
	nethttp "net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	kratosmetadata "github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/request"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	grpcmetadata "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type discardLogger struct{}

func (discardLogger) Log(kratoslog.Level, ...any) error { return nil }

func newTestRealBuilder(t testing.TB, discovery registry.Discovery) *builder {
	t.Helper()
	var resolver DiscoveryResolver
	if discovery != nil {
		resolver = testDiscoveryResolver{discovery}
	}
	builder := newBuilder(
		newTestLogger(discardLogger{}),
		appinfo.New("test"),
		newTestTracingProvider(t),
		newTestMetricsProvider(t),
		resolver,
	)
	return builder
}

func newTestTracingProvider(t testing.TB) tracing.Provider {
	t.Helper()
	disabled := true
	provider, cleanup, err := tracing.NewProvider(
		testconfig.New(t, "tracing", &config_pb.Tracing{Disable: &disabled}),
		appinfo.New("test"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if !provider.Disabled() {
		t.Fatal("test tracing provider is enabled")
	}
	return provider
}

func newTestMetricsProvider(t testing.TB) metrics.Provider {
	t.Helper()
	provider, cleanup, err := metrics.NewProvider(appinfo.New("test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return provider
}

func TestBuilderValidateConfigRejectsUnknownProtocol(t *testing.T) {
	t.Parallel()
	builder := newTestRealBuilder(t, nil)
	protocol := config_pb.Protocol(99)
	err := builder.validateConfig(&config_pb.Client{Clients: map[string]*config_pb.ClientOption{
		"orders": {Protocol: &protocol},
	}})
	if !errors.Is(err, ErrInvalidProtocol) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidProtocol)
	}
}

func TestBuilderBuildsDirectGRPCClient(t *testing.T) {
	t.Parallel()
	builder := newTestRealBuilder(t, nil)
	result, err := builder.build(context.Background(), newClientSpec("orders", &config_pb.ClientOption{
		Target: "passthrough:///orders",
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.close() })
	if result.grpcClient == nil || result.httpClient != nil {
		t.Fatal("gRPC build did not return exactly one gRPC client")
	}
}

func TestBuilderBuildsDirectHTTPClients(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		protocol config_pb.Protocol
		target   string
	}{
		{name: "http", protocol: config_pb.Protocol_HTTP, target: "http://127.0.0.1:1"},
		{name: "https", protocol: config_pb.Protocol_HTTPS, target: "https://127.0.0.1:1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			protocol := tt.protocol
			builder := newTestRealBuilder(t, nil)
			result, err := builder.build(context.Background(), newClientSpec("orders", &config_pb.ClientOption{
				Protocol: &protocol,
				Target:   tt.target,
			}, nil))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = result.close() })
			if result.httpClient == nil || result.grpcClient != nil {
				t.Fatal("HTTP build did not return exactly one HTTP client")
			}
		})
	}
}

func TestBuilderDefaultTargetRequiresDiscovery(t *testing.T) {
	t.Parallel()
	builder := newTestRealBuilder(t, nil)
	_, err := builder.build(context.Background(), newClientSpec("orders", nil, nil))
	if !errors.Is(err, ErrDiscoveryNotInitialized) {
		t.Fatalf("error = %v, want %v", err, ErrDiscoveryNotInitialized)
	}
}

func TestBuilderUsesConstructionEnvironmentSnapshot(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	discoveryReady := make(chan struct{}, 1)
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		_, _ = w.Write([]byte("snapshot"))
	}))
	t.Cleanup(server.Close)
	builder := newTestRealBuilder(t, staticDiscovery{
		instances: []*registry.ServiceInstance{{
			Name: "orders", Endpoints: []string{server.URL},
			Metadata: map[string]string{appinfo.MetadataEnvironment: "dev"},
		}},
		ready: discoveryReady,
	})
	t.Setenv("APP_ENV", "prod")
	protocol := config_pb.Protocol_HTTP
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := builder.build(ctx, newClientSpec("orders", &config_pb.ClientOption{
		Protocol: &protocol, Target: "discovery:///orders",
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = result.close() }()
	receiveWithin(t, discoveryReady, "initial discovery update")
	request, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, "http://orders/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := result.httpClient.Do(request)
	if err != nil {
		t.Fatalf("construction environment route failed after process environment change: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "snapshot" {
		t.Fatalf("response body = %q", body)
	}
}

func TestBuilderHTTPPropagatesRequestDebug(t *testing.T) {
	received := make(chan string, 1)
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		received <- r.Header.Get("x-foundation-debug")
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte("{}")); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	builder := newTestRealBuilder(t, nil)
	for _, propagate := range []bool{true, false} {
		result, err := builder.build(t.Context(), newClientSpec("debug", &config_pb.ClientOption{
			Protocol: config_pb.Protocol_HTTP.Enum(), Target: server.URL,
			RequestDebug: &config_pb.Middleware_RequestDebug{Propagate: proto.Bool(propagate)},
		}, nil))
		if err != nil {
			t.Fatal(err)
		}
		for _, debug := range []bool{false, true} {
			ctx := t.Context()
			if debug {
				ctx = request.WithDebug(ctx)
			}
			err = result.httpClient.Invoke(ctx, nethttp.MethodGet, "/", nil, &map[string]any{})
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if debug && propagate {
				want = "1"
			}
			if got := <-received; got != want {
				t.Fatalf("debug=%v propagate=%v header=%q", debug, propagate, got)
			}
		}
		if err := result.close(); err != nil {
			t.Error(err)
		}
	}
}

func TestBuilderHTTPContextAndErrorRemainCompatibleWithV1Server(t *testing.T) {
	observed := make(chan legacyHTTPContext, 1)
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		tenant, _ := url.QueryUnescape(r.Header.Get("x-md-tenant"))
		parts := strings.Split(r.Header.Get("traceparent"), "-")
		traceID := ""
		if len(parts) == 4 {
			traceID = parts[1]
		}
		observed <- legacyHTTPContext{
			tenant:        tenant,
			traceID:       traceID,
			timeoutMillis: r.Header.Get("x-request-timeout-ms"),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(nethttp.StatusConflict)
		_, _ = w.Write([]byte(`{"code":40901,"message":"order already exists","data":{"order_id":"o-1"},"reason":"ORDER_EXISTS","metadata":{"reason_code":"40901","http_data":"{\"order_id\":\"o-1\"}"}}`))
	}))
	t.Cleanup(server.Close)

	result, err := newTestRealBuilder(t, nil).build(t.Context(), newClientSpec("legacy", &config_pb.ClientOption{
		Protocol: config_pb.Protocol_HTTP.Enum(),
		Target:   server.URL,
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.close() })

	traceID, err := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("b7ad6b7169203331")
	if err != nil {
		t.Fatal(err)
	}
	ctx := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	}))
	ctx = kratosmetadata.NewClientContext(ctx, kratosmetadata.Metadata{"x-md-tenant": {"acme+v2"}})
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	callErr := result.httpClient.Invoke(ctx, nethttp.MethodGet, "/orders/o-1", nil, &map[string]any{})
	restored := foundationerrors.FromError(callErr)
	if restored.Code != 409 || restored.Reason != "ORDER_EXISTS" || restored.ReasonCode() != 40901 {
		t.Fatalf("v1 HTTP error decoded by v2 client = %+v", restored)
	}
	select {
	case got := <-observed:
		timeoutBudget, parseErr := time.ParseDuration(got.timeoutMillis + "ms")
		if parseErr != nil || timeoutBudget <= 0 || timeoutBudget > time.Second || got.tenant != "acme+v2" || got.traceID != traceID.String() {
			t.Fatalf("v1 server observed v2 HTTP context = %+v, timeout parse error=%v", got, parseErr)
		}
	case <-time.After(time.Second):
		t.Fatal("v1 server did not observe v2 HTTP context")
	}
}

type legacyHTTPContext struct {
	tenant        string
	traceID       string
	timeoutMillis string
}

func TestBuilderGRPCContextAndErrorRemainCompatibleWithV1Server(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	observed := make(chan legacyGRPCContext, 1)
	server := grpc.NewServer()
	healthpb.RegisterHealthServer(server, &legacyGRPCHealthServer{observed: observed})
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
		if serveErr := <-serveDone; serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			t.Error(serveErr)
		}
	})

	result, err := newTestRealBuilder(t, nil).build(t.Context(), newClientSpec("legacy", &config_pb.ClientOption{
		Target: "passthrough:///" + listener.Addr().String(),
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.close() })

	traceID, err := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("b7ad6b7169203331")
	if err != nil {
		t.Fatal(err)
	}
	ctx := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	}))
	ctx = kratosmetadata.NewClientContext(ctx, kratosmetadata.Metadata{"x-md-tenant": {"acme+v2"}})
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	_, callErr := healthpb.NewHealthClient(result.grpcClient).Check(ctx, &healthpb.HealthCheckRequest{})
	restored := foundationerrors.FromError(callErr)
	if restored.Code != 409 || restored.Reason != "ORDER_EXISTS" || restored.ReasonCode() != 40901 {
		t.Fatalf("v1 gRPC error decoded by v2 client = %+v", restored)
	}
	select {
	case got := <-observed:
		if !got.hasDeadline || got.tenant != "acme+v2" || got.traceID != traceID.String() {
			t.Fatalf("v1 server observed v2 gRPC context = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("v1 server did not observe v2 gRPC context")
	}
}

type legacyGRPCHealthServer struct {
	healthpb.UnimplementedHealthServer
	observed chan<- legacyGRPCContext
}

type legacyGRPCContext struct {
	hasDeadline bool
	tenant      string
	traceID     string
}

func (s *legacyGRPCHealthServer) Check(ctx context.Context, _ *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	_, hasDeadline := ctx.Deadline()
	md, _ := grpcmetadata.FromIncomingContext(ctx)
	tenant := ""
	if values := md.Get("x-md-tenant"); len(values) > 0 {
		tenant, _ = url.QueryUnescape(values[0])
	}
	traceID := ""
	if values := md.Get("traceparent"); len(values) > 0 {
		parts := strings.Split(values[0], "-")
		if len(parts) == 4 {
			traceID = parts[1]
		}
	}
	s.observed <- legacyGRPCContext{hasDeadline: hasDeadline, tenant: tenant, traceID: traceID}
	wireStatus, err := status.New(codes.Aborted, "order already exists").WithDetails(&errdetails.ErrorInfo{
		Reason:   "ORDER_EXISTS",
		Metadata: map[string]string{"reason_code": "40901"},
	})
	if err != nil {
		return nil, err
	}
	return nil, wireStatus.Err()
}

// 测试替身只提供内存发现能力，生产入口不再接收单个 Discovery。
type testDiscoveryResolver struct{ discovery registry.Discovery }

func (r testDiscoveryResolver) Discovery(string) (registry.Discovery, error) { return r.discovery, nil }
