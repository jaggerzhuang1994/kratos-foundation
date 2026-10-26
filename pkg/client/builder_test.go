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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	kratosmetadata "github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/observability"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
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
	}}, observability.Defaults{})
	if !errors.Is(err, ErrInvalidProtocol) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidProtocol)
	}
}

func TestBuilderBuildsDirectGRPCClient(t *testing.T) {
	t.Parallel()
	builder := newTestRealBuilder(t, nil)
	result, err := builder.build(context.Background(), newClientSpec("orders", &config_pb.ClientOption{
		Target: "passthrough:///orders",
	}, nil, observability.Defaults{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.close() })
	if result.grpcClient == nil || result.httpClient != nil {
		t.Fatal("gRPC build did not return exactly one gRPC client")
	}
}

func TestGRPCUnavailableContextPreservesStatusAndCause(t *testing.T) {
	if err := grpcUnavailableContext("orders")(
		context.Background(), "/orders.OrderService/Get", nil, nil, nil,
		func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
			return nil
		},
	); err != nil {
		t.Fatalf("successful RPC returned an error: %v", err)
	}
	cause := status.Error(codes.Unavailable, "dial tcp 10.0.5.121:9000: no route to host")
	err := grpcUnavailableContext("orders")(
		context.Background(), "/orders.OrderService/Get", nil, nil, nil,
		func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
			return cause
		},
	)
	if status.Code(err) != codes.Unavailable || !errors.Is(err, cause) ||
		!strings.Contains(err.Error(), `client "orders"`) ||
		!strings.Contains(err.Error(), "/orders.OrderService/Get") ||
		!strings.Contains(err.Error(), "no route to host") {
		t.Fatalf("unavailable RPC error lost client context or status: %v", err)
	}
	other := status.Error(codes.Aborted, "order already exists")
	got := grpcUnavailableContext("orders")(
		context.Background(), "/orders.OrderService/Get", nil, nil, nil,
		func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
			return other
		},
	)
	if got != other {
		t.Fatalf("business status was changed: %v", got)
	}
	businessStatus, err := status.New(codes.Unavailable, "upstream is busy").WithDetails(&errdetails.ErrorInfo{Reason: "BUSY"})
	if err != nil {
		t.Fatal(err)
	}
	businessCause := businessStatus.Err()
	got = grpcUnavailableContext("orders")(
		context.Background(), "/orders.OrderService/Get", nil, nil, nil,
		func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
			return businessCause
		},
	)
	if got != businessCause {
		t.Fatalf("detailed business status was changed: %v", got)
	}
}

func TestBuilderGRPCUnavailableIncludesClientName(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	healthpb.RegisterHealthServer(server, unavailableGRPCHealthServer{})
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		if serveErr := <-serveDone; serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			t.Error(serveErr)
		}
	})

	result, err := newTestRealBuilder(t, nil).build(t.Context(), newClientSpec("orders", &config_pb.ClientOption{
		Target: "passthrough:///" + listener.Addr().String(),
	}, nil, observability.Defaults{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.close() })
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err = healthpb.NewHealthClient(result.grpcClient).Check(ctx, &healthpb.HealthCheckRequest{})
	if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), `client "orders"`) ||
		!strings.Contains(err.Error(), "/grpc.health.v1.Health/Check") {
		t.Fatalf("gRPC call error lacks client context: %v", err)
	}
}

type unavailableGRPCHealthServer struct {
	healthpb.UnimplementedHealthServer
}

func (unavailableGRPCHealthServer) Check(context.Context, *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	return nil, status.Error(codes.Unavailable, "dial tcp 10.0.5.121:9000: no route to host")
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
			}, nil, observability.Defaults{}))
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
	_, err := builder.build(context.Background(), newClientSpec("orders", nil, nil, observability.Defaults{}))
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
	}, nil, observability.Defaults{}))
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
		}, nil, observability.Defaults{}))
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
	}, nil, observability.Defaults{}))
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
	}, nil, observability.Defaults{}))
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

func TestBuilderHTTPSDiscoveryUsesTLS(t *testing.T) {
	discoveryReady := make(chan struct{}, 1)
	server := httptest.NewTLSServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.TLS == nil {
			t.Error("request did not use TLS")
		}
		if r.URL.Path != "/health" {
			w.WriteHeader(nethttp.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(server.Close)

	previousDefaultTransport := nethttp.DefaultTransport
	nethttp.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { nethttp.DefaultTransport = previousDefaultTransport })

	protocol := config_pb.Protocol_HTTPS
	builder := newTestRealBuilder(t, staticDiscovery{
		instances: []*registry.ServiceInstance{{
			Name:      "orders",
			Endpoints: []string{server.URL},
			Metadata: map[string]string{
				appinfo.MetadataEnvironment: env.AppEnv(),
			},
		}},
		ready: discoveryReady,
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := builder.build(ctx, newClientSpec("orders", &config_pb.ClientOption{
		Protocol: &protocol,
		Target:   "discovery:///orders",
	}, nil, observability.Defaults{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.close() })
	receiveWithin(t, discoveryReady, "initial discovery update")

	requestContext, requestCancel := context.WithTimeout(context.Background(), time.Second)
	defer requestCancel()
	request, err := nethttp.NewRequestWithContext(requestContext, nethttp.MethodGet, "https://orders/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := result.httpClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" {
		t.Fatalf("response body = %q, want ok", body)
	}
}

func TestBuilderCloseClosesPrivateHTTPSTransportIdleConnections(t *testing.T) {
	idleConnections := make(chan struct{}, 1)
	closedConnections := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	server.Config.ConnState = func(_ net.Conn, state nethttp.ConnState) {
		switch state {
		case nethttp.StateIdle:
			select {
			case idleConnections <- struct{}{}:
			default:
			}
		case nethttp.StateClosed:
			select {
			case closedConnections <- struct{}{}:
			default:
			}
		}
	}
	server.StartTLS()
	t.Cleanup(server.Close)

	previousDefaultTransport := nethttp.DefaultTransport
	nethttp.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { nethttp.DefaultTransport = previousDefaultTransport })

	protocol := config_pb.Protocol_HTTPS
	builder := newTestRealBuilder(t, nil)
	result, err := builder.build(context.Background(), newClientSpec("orders", &config_pb.ClientOption{
		Protocol: &protocol,
		Target:   server.URL,
	}, nil, observability.Defaults{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.close() })

	requestContext, requestCancel := context.WithTimeout(context.Background(), time.Second)
	defer requestCancel()
	request, err := nethttp.NewRequestWithContext(requestContext, nethttp.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := result.httpClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-idleConnections:
	case <-time.After(time.Second):
		t.Fatal("HTTPS connection did not become idle")
	}

	if err := result.close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closedConnections:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("private HTTPS transport did not close its idle connection")
	}
}

type roundTripperFunc func(*nethttp.Request) (*nethttp.Response, error)

func (f roundTripperFunc) RoundTrip(request *nethttp.Request) (*nethttp.Response, error) {
	return f(request)
}

func TestBuilderHTTPSUsesCustomDefaultRoundTripper(t *testing.T) {
	var requests atomic.Int32
	transport := roundTripperFunc(func(request *nethttp.Request) (*nethttp.Response, error) {
		requests.Add(1)
		if request.URL.Scheme != "https" {
			t.Errorf("request scheme = %q, want https", request.URL.Scheme)
		}
		return &nethttp.Response{
			StatusCode: nethttp.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(nethttp.Header),
			Request:    request,
		}, nil
	})
	previousDefaultTransport := nethttp.DefaultTransport
	nethttp.DefaultTransport = transport
	t.Cleanup(func() { nethttp.DefaultTransport = previousDefaultTransport })

	protocol := config_pb.Protocol_HTTPS
	builder := newTestRealBuilder(t, nil)
	result, err := builder.build(context.Background(), newClientSpec("orders", &config_pb.ClientOption{
		Protocol: &protocol,
		Target:   "https://orders.example",
	}, nil, observability.Defaults{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.close() })

	requestContext, requestCancel := context.WithTimeout(context.Background(), time.Second)
	defer requestCancel()
	request, err := nethttp.NewRequestWithContext(requestContext, nethttp.MethodGet, "https://orders.example/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := result.httpClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("round trip count = %d, want 1", got)
	}
}

type emptyDiscovery struct{}

func (emptyDiscovery) GetService(context.Context, string) ([]*registry.ServiceInstance, error) {
	return nil, nil
}

func (emptyDiscovery) Watch(ctx context.Context, _ string) (registry.Watcher, error) {
	return &emptyWatcher{ctx: ctx, stopped: make(chan struct{})}, nil
}

type emptyWatcher struct {
	ctx       context.Context
	delivered atomic.Bool
	stopOnce  sync.Once
	stopped   chan struct{}
}

func (w *emptyWatcher) Next() ([]*registry.ServiceInstance, error) {
	if w.delivered.CompareAndSwap(false, true) {
		return nil, nil
	}
	select {
	case <-w.ctx.Done():
		return nil, w.ctx.Err()
	case <-w.stopped:
		return nil, context.Canceled
	}
}

func (w *emptyWatcher) Stop() error {
	w.stopOnce.Do(func() { close(w.stopped) })
	return nil
}

func TestBuilderAcceptsInjectedDiscovery(t *testing.T) {
	t.Parallel()
	builder := newTestRealBuilder(t, emptyDiscovery{})
	result, err := builder.build(context.Background(), newClientSpec("orders", nil, nil, observability.Defaults{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.close() })
	if result.grpcClient == nil || result.httpClient != nil {
		t.Fatal("discovery build did not return exactly one gRPC client")
	}
}

func TestBuilderHTTPDiscoveryDoesNotWaitForInitialNodes(t *testing.T) {
	discovery := newUpdatingHTTPDiscovery()
	builder := newTestRealBuilder(t, discovery)
	protocol := config_pb.Protocol_HTTP
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	result, err := builder.build(ctx, newClientSpec("orders", &config_pb.ClientOption{
		Protocol: &protocol,
		Target:   "discovery:///orders",
	}, nil, observability.Defaults{}))
	if err != nil {
		t.Fatalf("build waited for an initial discovery node: %v", err)
	}
	defer func() { _ = result.close() }()
	if result.httpClient == nil || result.grpcClient != nil {
		t.Fatal("discovery build did not return exactly one HTTP client")
	}
	receiveWithin(t, discovery.watchers, "HTTP discovery watcher")
}

type staticDiscovery struct {
	instances []*registry.ServiceInstance
	ready     chan struct{}
}

func (d staticDiscovery) GetService(context.Context, string) ([]*registry.ServiceInstance, error) {
	return d.instances, nil
}

func (d staticDiscovery) Watch(ctx context.Context, _ string) (registry.Watcher, error) {
	return &staticWatcher{
		ctx:       ctx,
		instances: d.instances,
		ready:     d.ready,
		stopped:   make(chan struct{}),
	}, nil
}

type staticWatcher struct {
	ctx       context.Context
	instances []*registry.ServiceInstance
	delivered atomic.Bool
	ready     chan struct{}
	stopOnce  sync.Once
	stopped   chan struct{}
}

func (w *staticWatcher) Next() ([]*registry.ServiceInstance, error) {
	if w.delivered.CompareAndSwap(false, true) {
		return w.instances, nil
	}
	// resolver 只有消费完首次返回的节点后才会再次调用 Next，此信号用于无 sleep 地同步测试请求。
	if w.ready != nil {
		select {
		case w.ready <- struct{}{}:
		default:
		}
	}
	select {
	case <-w.ctx.Done():
		return nil, w.ctx.Err()
	case <-w.stopped:
		return nil, context.Canceled
	}
}

func (w *staticWatcher) Stop() error {
	w.stopOnce.Do(func() { close(w.stopped) })
	return nil
}

type updatingHTTPDiscovery struct {
	updates     chan []*registry.ServiceInstance
	watchers    chan *updatingHTTPWatcher
	nextEntered chan struct{}
	stopErr     error
}

func newUpdatingHTTPDiscovery() *updatingHTTPDiscovery {
	return &updatingHTTPDiscovery{
		updates:     make(chan []*registry.ServiceInstance, 2),
		watchers:    make(chan *updatingHTTPWatcher, 1),
		nextEntered: make(chan struct{}, 4),
	}
}

func (d *updatingHTTPDiscovery) GetService(context.Context, string) ([]*registry.ServiceInstance, error) {
	return nil, nil
}

func (d *updatingHTTPDiscovery) Watch(ctx context.Context, _ string) (registry.Watcher, error) {
	watcher := &updatingHTTPWatcher{
		ctx:         ctx,
		updates:     d.updates,
		nextEntered: d.nextEntered,
		stopped:     make(chan struct{}),
		stopErr:     d.stopErr,
	}
	d.watchers <- watcher
	return watcher, nil
}

type updatingHTTPWatcher struct {
	ctx         context.Context
	updates     <-chan []*registry.ServiceInstance
	nextEntered chan<- struct{}
	stopOnce    sync.Once
	stopped     chan struct{}
	stopErr     error
}

func (w *updatingHTTPWatcher) Next() ([]*registry.ServiceInstance, error) {
	w.nextEntered <- struct{}{}
	select {
	case instances := <-w.updates:
		return instances, nil
	case <-w.ctx.Done():
		return nil, w.ctx.Err()
	case <-w.stopped:
		return nil, context.Canceled
	}
}

func (w *updatingHTTPWatcher) Stop() error {
	w.stopOnce.Do(func() { close(w.stopped) })
	return w.stopErr
}

func TestFactoryHTTPDiscoveryWatcherLivesUntilClientClose(t *testing.T) {
	firstServer := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		_, _ = w.Write([]byte("first"))
	}))
	t.Cleanup(firstServer.Close)
	secondServer := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		_, _ = w.Write([]byte("second"))
	}))
	t.Cleanup(secondServer.Close)

	discovery := newUpdatingHTTPDiscovery()
	discovery.stopErr = errors.New("watcher stop failed")
	instances := func(endpoint string) []*registry.ServiceInstance {
		return []*registry.ServiceInstance{{
			Name:      "orders",
			Endpoints: []string{endpoint},
			Metadata: map[string]string{
				appinfo.MetadataEnvironment: env.AppEnv(),
			},
		}}
	}
	discovery.updates <- instances(firstServer.URL)

	protocol := config_pb.Protocol_HTTP
	initial := &config_pb.Client{Clients: map[string]*config_pb.ClientOption{
		"orders": {
			Protocol: &protocol,
			Target:   "discovery:///orders",
		},
	}}
	builder := newTestRealBuilder(t, discovery)
	clientFactory, cleanup, err := NewFactory(testconfig.New(t, "client", initial), builder.logger, appinfo.New("test"), builder.tracing, builder.metrics, builder.discoveries)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	f := clientFactory.(*factory)

	httpClient, _, release, err := clientFactory.AcquireClient(context.Background(), "orders")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	watcher := receiveWithin(t, discovery.watchers, "HTTP discovery watcher")
	receiveWithin(t, discovery.nextEntered, "initial discovery lookup")
	release()
	f.mu.Lock()
	build := f.slots["orders"].build
	f.mu.Unlock()
	if build != nil {
		receiveWithin(t, build.done, "physical HTTP client build")
	}
	receiveWithin(t, discovery.nextEntered, "post-build discovery watch")
	if err := watcher.ctx.Err(); err != nil {
		t.Fatalf("watch context after physical build = %v, want active", err)
	}

	discovery.updates <- instances(secondServer.URL)
	receiveWithin(t, discovery.nextEntered, "watch after discovery update")

	cachedHTTPClient, _, cachedRelease, err := clientFactory.AcquireClient(context.Background(), "orders")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cachedRelease)
	if cachedHTTPClient != httpClient {
		t.Fatal("discovery update replaced the cached HTTP client")
	}
	requireResponseBody := func(want string) {
		t.Helper()
		requestContext, requestCancel := context.WithTimeout(context.Background(), time.Second)
		defer requestCancel()
		request, requestErr := nethttp.NewRequestWithContext(
			requestContext,
			nethttp.MethodGet,
			"http://orders/value",
			nil,
		)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		response, requestErr := cachedHTTPClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if got := string(body); got != want {
			t.Fatalf("response body = %q, want %q", got, want)
		}
	}
	requireResponseBody("second")

	cleanupDone := make(chan struct{})
	go func() {
		cleanup()
		close(cleanupDone)
	}()
	waitForFactoryClosed(t, f)
	if err := watcher.ctx.Err(); err != nil {
		t.Fatalf("watch context while cleanup waits for release = %v, want active", err)
	}

	discovery.updates <- instances(firstServer.URL)
	receiveWithin(t, discovery.nextEntered, "watch during cleanup")
	requireResponseBody("first")

	cachedRelease()
	receiveWithin(t, cleanupDone, "cleanup after cached release")
	receiveWithin(t, watcher.stopped, "HTTP discovery watcher stop")
	receiveWithin(t, watcher.ctx.Done(), "HTTP discovery watch context cancellation")
}
