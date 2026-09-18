package metadata

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	kratosmetadata "github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/transport"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	deadlinemiddleware "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/deadline"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

type headerCarrier http.Header

func (h headerCarrier) Get(k string) string { return http.Header(h).Get(k) }
func (h headerCarrier) Set(k, v string)     { http.Header(h).Set(k, v) }
func (h headerCarrier) Add(k, v string)     { http.Header(h).Add(k, v) }
func (h headerCarrier) Keys() []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	return keys
}
func (h headerCarrier) Values(k string) []string { return http.Header(h).Values(k) }

type testTransport struct {
	header  headerCarrier
	request *http.Request
}

func (t *testTransport) Kind() transport.Kind            { return transport.KindHTTP }
func (t *testTransport) Endpoint() string                { return "" }
func (t *testTransport) Operation() string               { return "" }
func (t *testTransport) RequestHeader() transport.Header { return t.header }
func (t *testTransport) ReplyHeader() transport.Header   { return t.header }
func (t *testTransport) Request() *http.Request          { return t.request }
func (t *testTransport) PathTemplate() string            { return "" }

var _ kratoshttp.Transporter = (*testTransport)(nil)

func TestValidateRejectsUnsafePrefixes(t *testing.T) {
	for _, prefix := range []string{"", " ", " x-md-", "x-md- "} {
		if Validate(&config_pb.Middleware_Metadata{Prefix: []string{prefix}}) == nil {
			t.Fatalf("prefix %q was accepted", prefix)
		}
	}
	if err := Validate(nil); err != nil {
		t.Fatalf("nil config: %v", err)
	}
	if err := Validate(&config_pb.Middleware_Metadata{Prefix: []string{"x-md-"}}); err != nil {
		t.Fatalf("valid prefix: %v", err)
	}
}

func TestServerPropagatesAllowedHeadersConstantsAndWebSocketCarriers(t *testing.T) {
	req := &http.Request{Header: make(http.Header), URL: &url.URL{RawQuery: "x-md-query=a%2Bb&ignored=no"}}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Protocol", "x-md-proto, proto%20value")
	tr := &testTransport{header: headerCarrier{"X-Md-Header": {"hello+world"}, "Ignored": {"no"}, deadlinemiddleware.HTTPTimeoutHeader: {"10s"}}, request: req}
	ctx := transport.NewServerContext(context.Background(), tr)
	mw := Server(&config_pb.Middleware_Metadata{Prefix: []string{"x-md-"}, Constants: map[string]string{"constant": "value"}})
	_, err := mw(func(ctx context.Context, _ any) (any, error) {
		md, ok := kratosmetadata.FromServerContext(ctx)
		if !ok {
			t.Fatal("server metadata missing")
		}
		for key, want := range map[string]string{"constant": "value", "x-md-header": "hello world", "x-md-query": "a+b", "x-md-proto": "proto value"} {
			if got := md.Get(key); got != want {
				t.Fatalf("metadata %s = %q, want %q", key, got, want)
			}
		}
		if md.Get("ignored") != "" || md.Get(deadlinemiddleware.HTTPTimeoutHeader) != "" {
			t.Fatalf("unallowed metadata leaked: %v", md)
		}
		return "ok", nil
	})(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestClientCombinesSourcesEscapesValuesAndSkipsReserved(t *testing.T) {
	tr := &testTransport{header: headerCarrier{}}
	ctx := kratosmetadata.NewServerContext(context.Background(), kratosmetadata.Metadata{"x-md-server": {"server value"}, "private": {"no"}})
	ctx = kratosmetadata.NewClientContext(ctx, kratosmetadata.Metadata{"client": {"client value"}, deadlinemiddleware.HTTPTimeoutHeader: {"5s"}})
	ctx = transport.NewClientContext(ctx, tr)
	_, err := Client(&config_pb.Middleware_Metadata{Prefix: []string{"x-md-"}, Constants: map[string]string{"constant": "constant value"}})(func(context.Context, any) (any, error) { return nil, nil })(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"constant": "constant+value", "client": "client+value", "x-md-server": "server+value"} {
		if got := tr.header.Get(key); got != want {
			t.Fatalf("header %s = %q, want %q", key, got, want)
		}
	}
	if tr.header.Get("private") != "" || tr.header.Get(deadlinemiddleware.HTTPTimeoutHeader) != "" {
		t.Fatalf("forbidden headers leaked: %v", tr.header)
	}
}

func TestMetadataWireContractRemainsCompatibleWithV1(t *testing.T) {
	const value = "tenant+a b/中文"
	t.Run("v1 caller to v2 server", func(t *testing.T) {
		tr := &testTransport{header: headerCarrier{}}
		tr.header.Set("x-md-tenant", url.QueryEscape(value))
		ctx := transport.NewServerContext(context.Background(), tr)

		_, err := Server(nil)(func(ctx context.Context, _ any) (any, error) {
			md, ok := kratosmetadata.FromServerContext(ctx)
			if !ok || md.Get("x-md-tenant") != value {
				t.Fatalf("server metadata = %v, want tenant %q", md, value)
			}
			return nil, nil
		})(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("v2 caller to v1 server", func(t *testing.T) {
		tr := &testTransport{header: headerCarrier{}}
		ctx := kratosmetadata.NewClientContext(context.Background(), kratosmetadata.Metadata{
			"x-md-tenant": {value},
		})
		ctx = transport.NewClientContext(ctx, tr)

		_, err := Client(nil)(func(context.Context, any) (any, error) { return nil, nil })(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := url.QueryUnescape(tr.header.Get("x-md-tenant"))
		if err != nil {
			t.Fatal(err)
		}
		if decoded != value {
			t.Fatalf("legacy server decoded metadata = %q, want %q", decoded, value)
		}
	})
}

func TestMiddlewareDisabledAndMissingTransportPassThrough(t *testing.T) {
	if Server(&config_pb.Middleware_Metadata{Disable: boolp(true)}) != nil || Client(&config_pb.Middleware_Metadata{Disable: boolp(true)}) != nil {
		t.Fatal("disabled middleware must be nil")
	}
	called := false
	_, err := Server(nil)(func(ctx context.Context, _ any) (any, error) {
		called = true
		if _, ok := kratosmetadata.FromServerContext(ctx); ok {
			t.Fatal("metadata unexpectedly added")
		}
		return nil, nil
	})(context.Background(), nil)
	if err != nil || !called {
		t.Fatalf("err=%v called=%t", err, called)
	}
}

func boolp(v bool) *bool { return &v }

func TestFrameworkHeadersCannotUseGenericMetadata(t *testing.T) {
	keys := []string{
		deadlinemiddleware.HTTPTimeoutHeader,
		"grpc-timeout",
		"traceparent",
		"tracestate",
		"baggage",
		"x-md-service-name",
		"x-foundation-debug",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			cfg := &config_pb.Middleware_Metadata{
				Prefix:    []string{"x-", "grpc-", "trace", "baggage"},
				Constants: map[string]string{key: "forged"},
			}
			for _, side := range []string{"server", "client"} {
				t.Run(side, func(t *testing.T) {
					tr := &testTransport{header: headerCarrier{}}
					ctx := context.Background()
					mw := Server(cfg)
					if side == "server" {
						tr.header.Set(key, "forged")
						ctx = transport.NewServerContext(ctx, tr)
					} else {
						mw = Client(cfg)
						ctx = kratosmetadata.NewClientContext(ctx, kratosmetadata.Metadata{key: {"forged"}})
						ctx = kratosmetadata.NewServerContext(ctx, kratosmetadata.Metadata{key: {"forged"}})
						ctx = transport.NewClientContext(ctx, tr)
					}
					_, err := mw(func(ctx context.Context, _ any) (any, error) {
						if side == "server" {
							md, _ := kratosmetadata.FromServerContext(ctx)
							if md.Get(key) != "" {
								t.Fatalf("generic server metadata accepted reserved key %q", key)
							}
						} else if tr.header.Get(key) != "" {
							t.Fatalf("generic client metadata injected reserved key %q", key)
						}
						return nil, nil
					})(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}
