package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
)

// HandleRoute 经过服务端中间件链，使用自定义 operation，并提供路径参数、查询参数与请求体绑定；
// 原生 Route 注册的路由不经过中间件。
func TestHandleRouteRunsMiddlewareAndBindsRequest(t *testing.T) {
	var operations []string
	recordOperation := func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			if tr, ok := transport.FromServerContext(ctx); ok {
				operations = append(operations, tr.Operation())
			}
			return next(ctx, req)
		}
	}
	spec := NewSpec()
	spec.HTTP().Middleware(recordOperation).Register(
		HandleRoute(http.MethodPost, "/orders/{id}/cancel", "/orders.v1.Orders/Cancel", func(ctx context.Context, req *HTTPRequest) (any, error) {
			var body struct{ Reason string }
			if err := req.Bind(&body); err != nil {
				return nil, err
			}
			if ctx != req.Context() {
				t.Error("request context differs from handler context")
			}
			return map[string]string{"id": req.PathParam("id"), "reason": body.Reason, "source": req.Query("source"), "missing": req.PathParam("missing")}, nil
		}),
		HandleRoute(http.MethodGet, "/plain/{id}", "", func(ctx context.Context, req *HTTPRequest) (any, error) {
			var query struct {
				Page int `json:"page"`
			}
			return query, req.BindQuery(&query)
		}),
		func(srv HTTPServer) error {
			srv.Route("/").GET("/native", func(ctx kratoshttp.Context) error { return ctx.String(http.StatusOK, "native") })
			return nil
		},
	)
	if HandleRoute(http.MethodGet, "/nil", "", nil) != nil {
		t.Fatal("nil handler produced an endpoint")
	}
	config, err := loadConfig(testconfig.Empty(t))
	if err != nil {
		t.Fatal(err)
	}
	server, err := newHTTPServer(config, newHTTPServerOptions(config, nil, spec), spec, newRuntimeTestLogger(t), newWebSocketHub())
	if err != nil {
		t.Fatal(err)
	}
	serve := func(method, target, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, request)
		return recorder
	}
	if r := serve(http.MethodPost, "/orders/42/cancel?source=app", `{"reason":"late"}`); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"id":"42"`) || !strings.Contains(r.Body.String(), `"reason":"late"`) || !strings.Contains(r.Body.String(), `"source":"app"`) || !strings.Contains(r.Body.String(), `"missing":""`) {
		t.Fatalf("cancel = %d %s", r.Code, r.Body)
	}
	if r := serve(http.MethodGet, "/plain/7?page=3", ""); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"page":3`) {
		t.Fatalf("plain = %d %s", r.Code, r.Body)
	}
	if r := serve(http.MethodPost, "/orders/42/cancel", `{bad`); r.Code != http.StatusBadRequest {
		t.Fatalf("bad body = %d %s", r.Code, r.Body)
	}
	if r := serve(http.MethodGet, "/native", ""); r.Code != http.StatusOK {
		t.Fatalf("native = %d", r.Code)
	}
	if strings.Join(operations, ",") != "/orders.v1.Orders/Cancel,/plain/{id},/orders.v1.Orders/Cancel" {
		t.Fatalf("middleware saw operations %v (native route must not appear)", operations)
	}
}
