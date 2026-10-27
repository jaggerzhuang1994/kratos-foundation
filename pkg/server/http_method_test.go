package server

import (
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/go-kratos/kratos/v2/transport"
	kratosgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/prometheus/client_golang/prometheus"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"
)

const (
	methodCancel  = "/http_method_service.MethodService/Cancel"
	methodPlain   = "/http_method_service.MethodService/Plain"
	methodJoin    = "/http_method_service.MethodService/Join"
	methodFeed    = "/http_method_service.MethodService/Feed"
	methodUnary   = "/http_method_service.MethodService/Unary"
	methodMultiWS = "/http_method_service.MethodService/MultiWS"
)

var registerHTTPMethodProto sync.Once

// useHTTPMethodTestProto 在全局描述符表中登记一次测试 proto，与生成代码在包初始化时登记的方式一致。
func useHTTPMethodTestProto(t *testing.T) {
	t.Helper()
	registerHTTPMethodProto.Do(func() {
		registerTestProto(t, protoregistry.GlobalFiles, "http_method_service/methods.proto", "http_method_service", "MethodService",
			testMethod{name: "Cancel", rule: &annotations.HttpRule{
				Pattern:            &annotations.HttpRule_Post{Post: "/orders/{id}:cancel"},
				Body:               "*",
				AdditionalBindings: []*annotations.HttpRule{get("/orders/{id}/cancel")},
			}},
			testMethod{name: "Plain"},
			testMethod{name: "Join", rule: get("/method-ws/join"), streaming: true},
			testMethod{name: "Feed", rule: post("/method-ws/feed"), streaming: true},
			testMethod{name: "Unary", rule: get("/method-ws/unary")},
			testMethod{name: "MultiWS", rule: &annotations.HttpRule{
				Pattern:            &annotations.HttpRule_Get{Get: "/method-ws/a"},
				AdditionalBindings: []*annotations.HttpRule{get("/method-ws/b")},
			}, streaming: true},
		)
	})
}

func TestResolveProtoHTTPMethod(t *testing.T) {
	useHTTPMethodTestProto(t)
	resolved, err := resolveProtoHTTPMethod(protoregistry.GlobalFiles, strings.TrimPrefix(methodCancel, "/"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.operation != methodCancel || !slices.Equal(resolved.routes, []httpRoute{
		{method: nethttp.MethodPost, path: "/orders/{id}:cancel"},
		{method: nethttp.MethodGet, path: "/orders/{id}/cancel"},
	}) {
		t.Fatalf("resolved = %q %v", resolved.operation, resolved.routes)
	}
	for method, want := range map[string]string{
		"no-slash":                     "must be /<package.Service>/<Method>",
		"/unknown_service.Service/Get": "is not registered",
		"/http_method_service.MethodService/Missing": "has no method Missing",
		methodPlain: "has no google.api.http rule",
		"/http_method_service.MethodService/Cancel/More": "must be /<package.Service>/<Method>",
	} {
		if _, err := resolveProtoHTTPMethod(protoregistry.GlobalFiles, method); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("resolve %q error = %v, want %q", method, err, want)
		}
	}
}

func TestHandleMethodMountsDeclaredRoutesWithOperation(t *testing.T) {
	useHTTPMethodTestProto(t)
	srv := kratoshttp.NewServer()
	var gotID, gotOperation string
	endpoint := HandleMethod(methodCancel, func(ctx context.Context, request *HTTPRequest) (any, error) {
		gotID = request.PathParam("id")
		if info, ok := transport.FromServerContext(ctx); ok {
			gotOperation = info.Operation()
		}
		return map[string]string{"ok": "yes"}, nil
	})
	if err := endpoint(srv); err != nil {
		t.Fatal(err)
	}
	var routes []httpRoute
	if err := srv.WalkRoute(func(info kratoshttp.RouteInfo) error {
		routes = append(routes, httpRoute{method: info.Method, path: info.Path})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(routes, []httpRoute{{method: nethttp.MethodPost, path: "/orders/{id}:cancel"}, {method: nethttp.MethodGet, path: "/orders/{id}/cancel"}}) {
		t.Fatalf("routes = %v", routes)
	}
	recorder := httptest.NewRecorder()
	srv.ServeHTTP(recorder, httptest.NewRequest(nethttp.MethodPost, "/orders/42:cancel", strings.NewReader("{}")))
	if recorder.Code != nethttp.StatusOK || gotID != "42" || gotOperation != methodCancel {
		t.Fatalf("response = %d id = %q operation = %q", recorder.Code, gotID, gotOperation)
	}
}

func TestHandleMethodRejectsStreamingAndUnknownMethods(t *testing.T) {
	useHTTPMethodTestProto(t)
	handler := func(context.Context, *HTTPRequest) (any, error) { return nil, nil }
	if err := HandleMethod(methodJoin, handler)(kratoshttp.NewServer()); err == nil || !strings.Contains(err.Error(), "WebSocketMethod") {
		t.Fatalf("streaming method error = %v", err)
	}
	if err := HandleMethod(methodPlain, handler)(kratoshttp.NewServer()); err == nil {
		t.Fatal("method without google.api.http accepted")
	}
	if HandleMethod(methodCancel, nil) != nil {
		t.Fatal("nil handler should return a nil endpoint")
	}
}

func TestWebSocketMethodResolvesDeclaredGETRoute(t *testing.T) {
	useHTTPMethodTestProto(t)
	spec := NewSpec()
	spec.HTTP().WebSocketMethodWithConfig(methodJoin, runtimeHandshakeHandler{}, WebSocketConfig{MaxMessageBytes: 2048})
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	endpoint := spec.http.registrations[0].websocket
	if endpoint.path != "/method-ws/join" || endpoint.operation() != methodJoin || endpoint.maxMessageBytes != 2048 {
		t.Fatalf("endpoint = path %q operation %q max %d", endpoint.path, endpoint.operation(), endpoint.maxMessageBytes)
	}
	for method, want := range map[string]string{
		methodUnary:   "must be a streaming rpc",
		methodFeed:    "exactly one GET",
		methodMultiWS: "exactly one GET",
		methodPlain:   "has no google.api.http rule",
	} {
		invalid := NewSpec()
		invalid.HTTP().WebSocketMethod(method, runtimeHandshakeHandler{})
		if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("WebSocketMethod(%s) Validate = %v, want %q", method, err, want)
		}
	}
}

func TestWebSocketGRPCConflicts(t *testing.T) {
	useHTTPMethodTestProto(t)
	spec := NewSpec()
	spec.HTTP().WebSocketMethod(methodJoin, runtimeHandshakeHandler{}).WebSocket("/plain-ws", runtimeHandshakeHandler{})
	clean := kratosgrpc.NewServer()
	if conflicts := websocketGRPCConflicts(spec, clean); len(conflicts) != 0 {
		t.Fatalf("conflicts without registration = %v", conflicts)
	}
	conflicting := kratosgrpc.NewServer()
	registerTestGRPCService(conflicting.Server, "http_method_service.MethodService")
	if conflicts := websocketGRPCConflicts(spec, conflicting); !slices.Equal(conflicts, []string{methodJoin}) {
		t.Fatalf("conflicts = %v", conflicts)
	}
	if conflicts := websocketGRPCConflicts(spec, nil); conflicts != nil {
		t.Fatalf("conflicts with gRPC disabled = %v", conflicts)
	}
}

// 按 proto 声明挂载的手写路由与 WebSocket 都归入声明它们的服务名，不再记为未映射。
func TestProtoMountedRoutesMapToServiceName(t *testing.T) {
	useHTTPMethodTestProto(t)
	spec := NewSpec()
	spec.HTTP().
		Register(HandleMethod(methodCancel, func(context.Context, *HTTPRequest) (any, error) { return nil, nil })).
		WebSocketMethod(methodJoin, runtimeHandshakeHandler{})
	config := proto.CloneOf(defaultConfig)
	config.Http.Addr = proto.String("127.0.0.1:0")
	config.Http.Metrics.Addr = proto.String("localhost:0")
	config.Http.Health.Addr = proto.String("localhost:0")
	runtime, cleanup, err := NewRuntime(testconfig.New(t, "server", config), newRuntimeTestLogger(t),
		testMetricsProvider{registry: prometheus.NewRegistry()},
		runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	httpServer, _ := runtime.Servers()
	names, unmapped, err := httpServer.(*serviceRuntime).ServiceNames()
	if err != nil || !slices.Equal(names, []string{"http_method_service"}) || len(unmapped) != 0 {
		t.Fatalf("names = %v unmapped = %q err = %v", names, unmapped, err)
	}
}
