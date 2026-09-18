package server

import (
	"net/http"
	"testing"
)

func TestNewSpecHasValidZeroValue(t *testing.T) {
	spec := NewSpec()
	if spec == nil {
		t.Fatal("NewSpec returned nil")
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

type testSocketHandler struct{}

func (testSocketHandler) OnClose(WebSocketConn) {}

func TestSpecBuildersIgnoreNilDeclarations(t *testing.T) {
	spec := NewSpec()
	spec.HTTP().Middleware(nil).Register(
		nil,
		HandleHTTP(http.MethodGet, "/nil", nil),
		HandleHTTPWriter(http.MethodGet, "/nil-writer", nil),
	).Option(nil).WebSocket("/ws", testSocketHandler{})
	spec.GRPC().Middleware(nil).Register(nil).Option(nil)
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(spec.http.middlewares) != 0 || len(spec.http.endpoints) != 0 || len(spec.http.options) != 0 || len(spec.grpc.middlewares) != 0 || len(spec.grpc.services) != 0 || len(spec.grpc.options) != 0 {
		t.Fatal("nil builder arguments must be ignored")
	}

}

func TestSpecMiddlewareBuildersKeepProtocolSpecificDefinitions(t *testing.T) {
	var calls []string
	httpMiddleware := marker("http", &calls)
	grpcMiddleware := marker("grpc", &calls)
	httpSpec := MiddlewareSpec{
		Name:       "http-custom",
		Priority:   MiddlewarePriorityMetadata + 1,
		Middleware: httpMiddleware,
	}
	grpcSpec := MiddlewareSpec{
		Name:       "grpc-custom",
		Priority:   MiddlewarePriorityTracing + 1,
		Middleware: grpcMiddleware,
	}

	spec := NewSpec()
	if got := spec.HTTP().MiddlewareSpec(httpSpec); got != &spec.http {
		t.Fatal("HTTP MiddlewareSpec must return the same builder")
	}
	if got := spec.GRPC().MiddlewareSpec(grpcSpec); got != &spec.grpc {
		t.Fatal("gRPC MiddlewareSpec must return the same builder")
	}

	if len(spec.http.middlewares) != 1 || spec.http.middlewares[0].Name != httpSpec.Name ||
		spec.http.middlewares[0].Priority != httpSpec.Priority || spec.http.middlewares[0].Middleware == nil {
		t.Fatalf("HTTP middleware specs = %#v", spec.http.middlewares)
	}
	if len(spec.grpc.middlewares) != 1 || spec.grpc.middlewares[0].Name != grpcSpec.Name ||
		spec.grpc.middlewares[0].Priority != grpcSpec.Priority || spec.grpc.middlewares[0].Middleware == nil {
		t.Fatalf("gRPC middleware specs = %#v", spec.grpc.middlewares)
	}

	// The builder copies the variadic elements into its own slice, so changing the
	// caller's value after registration cannot alter the recorded definition.
	httpSpec.Name = "changed"
	grpcSpec.Name = "changed"
	if spec.http.middlewares[0].Name != "http-custom" || spec.grpc.middlewares[0].Name != "grpc-custom" {
		t.Fatal("middleware definitions alias caller-owned values")
	}
}

func TestSpecValidateRejectsBadWebSocketDefinitions(t *testing.T) {
	cases := []struct {
		name string
		add  func(*Spec)
	}{
		{"relative path", func(s *Spec) { s.HTTP().WebSocket("ws", testSocketHandler{}) }},
		{"nil handler", func(s *Spec) { s.HTTP().WebSocket("/ws", nil) }},
		{"unsupported handler", func(s *Spec) { s.HTTP().WebSocket("/ws", struct{}{}) }},
		{"duplicate path", func(s *Spec) { s.HTTP().WebSocket("/ws", testSocketHandler{}).WebSocket("/ws", testSocketHandler{}) }},
		{"negative message limit", func(s *Spec) {
			s.HTTP().WebSocketWithConfig("/ws", testSocketHandler{}, WebSocketConfig{MaxMessageBytes: -2})
		}},
		{"negative in-flight message limit", func(s *Spec) {
			s.HTTP().WebSocketWithConfig("/ws", testSocketHandler{}, WebSocketConfig{MaxInFlightMessages: -1})
		}},
		{"two upgraders", func(s *Spec) { s.HTTP().WebSocket("/ws", testSocketHandler{}, Upgrader{}, Upgrader{}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSpec()
			tc.add(s)
			if err := s.Validate(); err == nil {
				t.Fatal("invalid websocket definition was accepted")
			}
		})
	}
}
