package client

import (
	"context"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"io"
	"net"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuilderHTTPSDiscoveryUsesTLS(t *testing.T) {
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
	builder := newTestRealBuilder(t, staticDiscovery{instances: []*registry.ServiceInstance{{
		Name:      "orders",
		Endpoints: []string{server.URL},
		Metadata: map[string]string{
			appinfo.MetadataEnvironment: env.AppEnv(),
		},
	}}})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := builder.build(ctx, newClientSpec("orders", &config_pb.ClientOption{
		Protocol: &protocol,
		Target:   "discovery:///orders",
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.close() })

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
	}, nil))
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
	}, nil))
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
