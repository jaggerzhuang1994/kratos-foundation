package client

import (
	"context"
	"errors"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/request"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"time"
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
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		_, _ = w.Write([]byte("snapshot"))
	}))
	t.Cleanup(server.Close)
	builder := newTestRealBuilder(t, staticDiscovery{instances: []*registry.ServiceInstance{{
		Name: "orders", Endpoints: []string{server.URL},
		Metadata: map[string]string{appinfo.MetadataEnvironment: "dev"},
	}}})
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
			Middleware: &config_pb.ClientMiddleware{RequestDebug: &config_pb.Middleware_RequestDebug{Propagate: proto.Bool(propagate)}},
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

// 测试替身只提供内存发现能力，生产入口不再接收单个 Discovery。
type testDiscoveryResolver struct{ discovery registry.Discovery }

func (r testDiscoveryResolver) Discovery(string) (registry.Discovery, error) { return r.discovery, nil }
