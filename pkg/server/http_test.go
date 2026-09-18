package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	kratosmetadata "github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/gorilla/mux"
	deadlinecontext "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/deadline"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestHandleHTTPUsesRequestMiddlewareAndResponseEncoder(t *testing.T) {
	type requestKey struct{}
	const operation = "/files/{id}"

	spec := NewSpec()
	spec.HTTP().Register(HandleHTTP(http.MethodPost, operation, func(request *http.Request) (any, error) {
		if got := request.Context().Value(requestKey{}); got != "upload" {
			t.Fatalf("middleware context value = %v, want upload", got)
		}
		if got := request.Header.Get("X-Middleware"); got != "replaced" {
			t.Fatalf("middleware request header = %q, want replaced", got)
		}
		info, ok := transport.FromServerContext(request.Context())
		if !ok || info.Operation() != operation {
			t.Fatalf("transport operation = %q, want %q", info.Operation(), operation)
		}
		return mux.Vars(request)["id"], nil
	}))

	server := kratoshttp.NewServer(
		kratoshttp.ResponseEncoder(func(w http.ResponseWriter, _ *http.Request, reply any) error {
			w.WriteHeader(http.StatusCreated)
			_, err := w.Write([]byte("encoded:" + reply.(string)))
			return err
		}),
		kratoshttp.Middleware(func(next middleware.Handler) middleware.Handler {
			return func(ctx context.Context, req any) (any, error) {
				request, ok := req.(*http.Request)
				if !ok {
					t.Fatalf("middleware request has type %T, want *http.Request", req)
				}
				ctx = context.WithValue(ctx, requestKey{}, "upload")
				request = request.Clone(ctx)
				request.Header.Set("X-Middleware", "replaced")
				return next(ctx, request)
			}
		}),
	)
	if err := spec.http.registrations[0].endpoint(server); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/files/42", nil))
	if response.Code != http.StatusCreated || response.Body.String() != "encoded:42" {
		t.Fatalf(
			"response = (%d, %q), want (%d, %q)",
			response.Code,
			response.Body.String(),
			http.StatusCreated,
			"encoded:42",
		)
	}
}

func TestHandleHTTPEncodesMiddlewareReply(t *testing.T) {
	var handlerCalled bool
	spec := NewSpec()
	spec.HTTP().Register(HandleHTTP(http.MethodGet, "/cached", func(*http.Request) (any, error) {
		handlerCalled = true
		return nil, nil
	}))

	server := kratoshttp.NewServer(kratoshttp.Middleware(func(middleware.Handler) middleware.Handler {
		return func(context.Context, any) (any, error) {
			return map[string]string{"source": "middleware"}, nil
		}
	}))
	if err := spec.http.registrations[0].endpoint(server); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/cached", nil))
	var reply map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil {
		t.Fatalf("decode middleware reply: %v", err)
	}
	if handlerCalled || reply["source"] != "middleware" {
		t.Fatalf("handler called = %t, reply = %v", handlerCalled, reply)
	}
}

func TestHandleHTTPReturnsErrorsToHTTPEncoder(t *testing.T) {
	wantErr := errors.New("upload failed")
	var encodedErr error
	spec := NewSpec()
	spec.HTTP().Register(HandleHTTP(http.MethodPost, "/upload", func(*http.Request) (any, error) {
		return nil, wantErr
	}))

	server := kratoshttp.NewServer(kratoshttp.ErrorEncoder(func(w http.ResponseWriter, _ *http.Request, err error) {
		encodedErr = err
		w.WriteHeader(http.StatusTeapot)
	}))
	if err := spec.http.registrations[0].endpoint(server); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/upload", nil))
	if !errors.Is(encodedErr, wantErr) || response.Code != http.StatusTeapot {
		t.Fatalf("encoded error = %v, status = %d", encodedErr, response.Code)
	}
}

func TestHandleHTTPWriterWritesCustomResponse(t *testing.T) {
	type requestKey struct{}
	const operation = "/files/{id}"

	spec := NewSpec()
	spec.HTTP().Register(HandleHTTPWriter(
		http.MethodPost,
		operation,
		func(w http.ResponseWriter, request *http.Request) error {
			if got := request.Context().Value(requestKey{}); got != "upload" {
				t.Fatalf("middleware context value = %v, want upload", got)
			}
			w.Header().Set("X-Handler", "writer")
			w.WriteHeader(http.StatusAccepted)
			_, err := w.Write([]byte(mux.Vars(request)["id"]))
			return err
		},
	))

	server := kratoshttp.NewServer(kratoshttp.Middleware(func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			request := req.(*http.Request)
			ctx = context.WithValue(ctx, requestKey{}, "upload")
			return next(ctx, request.Clone(ctx))
		}
	}))
	if err := spec.http.registrations[0].endpoint(server); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/files/42", nil))
	if response.Code != http.StatusAccepted {
		t.Fatalf("response status = %d, want %d", response.Code, http.StatusAccepted)
	}
	if response.Body.String() != "42" || response.Header().Get("X-Handler") != "writer" {
		t.Fatalf(
			"response body and header = (%q, %q), want (%q, %q)",
			response.Body.String(),
			response.Header().Get("X-Handler"),
			"42",
			"writer",
		)
	}
}

func TestHandleHTTPWriterReturnsErrorsToHTTPEncoder(t *testing.T) {
	wantErr := errors.New("writer failed")
	var encodedErr error
	spec := NewSpec()
	spec.HTTP().Register(HandleHTTPWriter(http.MethodPost, "/writer", func(http.ResponseWriter, *http.Request) error {
		return wantErr
	}))

	server := kratoshttp.NewServer(kratoshttp.ErrorEncoder(func(w http.ResponseWriter, _ *http.Request, err error) {
		encodedErr = err
		w.WriteHeader(http.StatusTeapot)
	}))
	if err := spec.http.registrations[0].endpoint(server); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/writer", nil))
	if !errors.Is(encodedErr, wantErr) || response.Code != http.StatusTeapot {
		t.Fatalf("encoded error = %v, status = %d", encodedErr, response.Code)
	}
}

func TestHandleHTTPWriterEncodesMiddlewareReply(t *testing.T) {
	var handlerCalled bool
	spec := NewSpec()
	spec.HTTP().Register(HandleHTTPWriter(
		http.MethodGet,
		"/cached-writer",
		func(http.ResponseWriter, *http.Request) error {
			handlerCalled = true
			return nil
		},
	))

	server := kratoshttp.NewServer(kratoshttp.Middleware(func(middleware.Handler) middleware.Handler {
		return func(context.Context, any) (any, error) {
			return map[string]string{"source": "middleware"}, nil
		}
	}))
	if err := spec.http.registrations[0].endpoint(server); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/cached-writer", nil))
	var reply map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil {
		t.Fatalf("decode middleware reply: %v", err)
	}
	if handlerCalled || reply["source"] != "middleware" {
		t.Fatalf("handler called = %t, reply = %v", handlerCalled, reply)
	}
}

func TestHTTPAndWebSocketRoutesFollowSpecRegistrationOrder(t *testing.T) {
	spec := NewSpec()
	spec.HTTP().
		Register(HandleHTTP(http.MethodGet, "/first", func(*http.Request) (any, error) { return nil, nil })).
		WebSocket("/socket", testSocketHandler{}).
		Register(HandleHTTP(http.MethodGet, "/last", func(*http.Request) (any, error) { return nil, nil }))

	server, err := newHTTPServer(nil, nil, spec, newRuntimeTestLogger(t), newWebSocketHub())
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	if err := server.WalkRoute(func(route kratoshttp.RouteInfo) error {
		paths = append(paths, route.Path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(paths, ","), "/first,/socket,/last"; got != want {
		t.Fatalf("route order = %q, want %q", got, want)
	}
}

// TestIntegrationHTTPRoutes 验证正式构造链、路由选项、业务中间件及错误编码器的组合契约。
func TestIntegrationHTTPRoutes(t *testing.T) {
	type request struct {
		Name string `json:"name"`
		Mode string `json:"mode"`
	}
	type requestKey struct{}
	spec := NewSpec()
	var handler HTTPServer
	spec.HTTP().Option(kratoshttp.PathPrefix("/v1"))
	spec.HTTP().Middleware(func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			return next(context.WithValue(ctx, requestKey{}, "orders"), req)
		}
	})
	spec.HTTP().Register(func(srv HTTPServer) error {
		handler = srv
		srv.Route("/orders").POST("/", func(ctx kratoshttp.Context) error {
			var input request
			if err := ctx.Bind(&input); err != nil {
				return err
			}
			// 原生 Route handler 须显式调用 Middleware，保持与生成 HTTP handler 一致。
			reply, err := ctx.Middleware(func(callCtx context.Context, req any) (any, error) {
				input := req.(*request)
				switch input.Mode {
				case "business-error":
					return nil, foundationerrors.New(http.StatusConflict, "ORDER_EXISTS", "order already exists").
						WithReasonCode(40901).WithHTTPHeaders(http.Header{"Retry-After": {"3"}})
				case "unexpected-error":
					return nil, errors.New("internal storage location /private/order.db")
				case "panic":
					panic("private panic details")
				case "context":
					md, _ := kratosmetadata.FromServerContext(callCtx)
					deadlineInfo, _ := deadlinecontext.InfoFromContext(callCtx)
					return map[string]any{
						"tenant":          md.Get("x-md-tenant"),
						"trace_id":        trace.SpanContextFromContext(callCtx).TraceID().String(),
						"deadline_source": string(deadlineInfo.Source),
					}, nil
				}
				return map[string]any{"name": input.Name, "module": callCtx.Value(requestKey{})}, nil
			})(ctx, &input)
			if err != nil {
				return err
			}
			return ctx.Result(http.StatusCreated, reply)
		})
		return nil
	})
	// PathPrefix 原生 option 逐层创建子路由，因此代码前缀与配置前缀叠加。
	manager := testconfig.New(t, "server", &config_pb.Server{Http: &config_pb.HttpServerOption{
		PathPrefix: strp("/configured"),
	}})
	runtime, cleanup, err := NewRuntime(manager, newRuntimeTestLogger(t),
		testMetricsProvider{registry: prometheus.NewRegistry()},
		runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if httpRuntime, grpcRuntime := runtime.Servers(); httpRuntime == nil || grpcRuntime != nil {
		t.Fatalf("protocol selection: HTTP=%v gRPC=%v", httpRuntime, grpcRuntime)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = runtimeTestTimeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	for _, test := range []struct {
		name, method, path, body string
		status                   int
		reason                   string
	}{
		{"json with middleware", http.MethodPost, "/configured/v1/orders", `{"name":"订单甲"}`, http.StatusCreated, ""},
		{"v1 context contract", http.MethodPost, "/configured/v1/orders", `{"mode":"context"}`, http.StatusCreated, ""},
		{"empty JSON object", http.MethodPost, "/configured/v1/orders", `{}`, http.StatusCreated, ""},
		{"business error", http.MethodPost, "/configured/v1/orders", `{"mode":"business-error"}`, http.StatusConflict, "ORDER_EXISTS"},
		{"unknown error redacted", http.MethodPost, "/configured/v1/orders", `{"mode":"unexpected-error"}`, http.StatusInternalServerError, "UNKNOWN"},
		{"panic recovered", http.MethodPost, "/configured/v1/orders", `{"mode":"panic"}`, http.StatusInternalServerError, "UNKNOWN"},
		{"malformed JSON", http.MethodPost, "/configured/v1/orders", `{"name":`, http.StatusBadRequest, ""},
		{"method not allowed", http.MethodGet, "/configured/v1/orders", "", http.StatusMethodNotAllowed, ""},
		{"missing configured prefix", http.MethodPost, "/v1/orders", `{}`, http.StatusNotFound, ""},
		{"strict slash redirect", http.MethodPost, "/configured/v1/orders/", `{}`, http.StatusMovedPermanently, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), test.method, server.URL+test.path, strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			if strings.Contains(test.body, `"mode":"context"`) {
				req.Header.Set("x-md-tenant", "acme%2Blegacy")
				req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
			}
			response, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.StatusCode, test.status, body)
			}
			if strings.Contains(string(body), "private") {
				t.Fatalf("internal error leaked: %s", body)
			}
			if test.status == http.StatusMovedPermanently {
				if response.Header.Get("Location") != "/configured/v1/orders" {
					t.Fatalf("redirect=%q", response.Header.Get("Location"))
				}
			}
			if test.status == http.StatusCreated || test.reason != "" {
				var result map[string]any
				if err := json.Unmarshal(body, &result); err != nil {
					t.Fatal(err)
				}
				if test.status == http.StatusCreated {
					var input request
					if err := json.Unmarshal([]byte(test.body), &input); err != nil {
						t.Fatal(err)
					}
					if input.Mode == "context" {
						if result["tenant"] != "acme+legacy" || result["trace_id"] != "0af7651916cd43dd8448eb211c80319c" || result["deadline_source"] != string(deadlinecontext.SourceFallback) {
							t.Fatalf("v1 HTTP context observed by v2 server=%v", result)
						}
					} else if result["name"] != input.Name || result["module"] != "orders" {
						t.Fatalf("response=%v", result)
					}
				} else if result["reason"] != test.reason {
					t.Fatalf("reason=%v want=%s", result["reason"], test.reason)
				}
				if test.status == http.StatusConflict && (result["code"] != float64(40901) || response.Header.Get("Retry-After") != "3") {
					t.Fatalf("business error contract: body=%s headers=%v", body, response.Header)
				}
			}
		})
	}
}
