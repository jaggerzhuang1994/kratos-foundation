package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/transport"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/gorilla/websocket"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestWebsocketHubServeAndStopAreConcurrencySafe(t *testing.T) {
	hub := newWebSocketHub()
	client := &websocketClient{}
	if !hub.serve(client) {
		t.Fatal("hub rejected client before stopping")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if err := hub.stop(ctx); err != nil && !strings.Contains(err.Error(), "websocket connection is nil") {
				t.Errorf("stop: %v", err)
			}
		})
	}
	wg.Wait()
	if hub.serve(&websocketClient{}) {
		t.Fatal("hub accepted client after stopping")
	}
}

func TestWebSocketServerHandlesConnectionMessagesAndReplies(t *testing.T) {
	logger := newRuntimeTestLogger(t)
	hub := newWebSocketHub()
	handler := newRuntimeWebSocketHandler()
	server := kratoshttp.NewServer()
	newWebSocketServer(logger, server, hub).Handle(
		"/ws",
		"/ws",
		handler,
		0,
		0,
		0,
		Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
	)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	client, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws",
		nil,
	)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	serverConnection := receiveRuntimeValue(t, handler.connected)
	if serverConnection.Request() == nil || serverConnection.Request().URL.Path != "/ws" {
		t.Fatalf("Request() = %#v", serverConnection.Request())
	}

	if err := client.WriteMessage(websocket.TextMessage, []byte("request")); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	message := receiveRuntimeValue(t, handler.messages)
	if message.kind != TextMessage || string(message.data) != "request" {
		t.Fatalf("message = %#v", message)
	}

	assertWebSocketReply(t, client, websocket.TextMessage, "text", func() error {
		return serverConnection.SendText("text")
	})
	assertWebSocketReply(t, client, websocket.BinaryMessage, "binary", func() error {
		return serverConnection.SendBinary([]byte("binary"))
	})
	assertWebSocketReply(t, client, websocket.TextMessage, `{"ok":true}`, func() error {
		return serverConnection.SendJSON(map[string]bool{"ok": true})
	})

	if err := client.Close(); err != nil {
		t.Fatalf("client.Close() error = %v", err)
	}
	receiveRuntimeValue(t, handler.closed)
	ctx, cancel := context.WithTimeout(context.Background(), runtimeTestTimeout)
	defer cancel()
	if err := hub.stop(ctx); err != nil {
		t.Fatalf("hub.stop() error = %v", err)
	}
}

func TestWebSocketHandshakeCanRejectBeforeUpgrade(t *testing.T) {
	logger := newRuntimeTestLogger(t)
	wantErr := errors.New("denied")
	request := httptest.NewRequest("GET", "http://example.test/ws", nil)
	response := httptest.NewRecorder()

	_, err := upgrade(
		Upgrader{},
		logger,
		request,
		response,
		runtimeHandshakeHandler{err: wantErr},
		0,
		0,
		0,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("upgrade() error = %v, want %v", err, wantErr)
	}
	newWebSocketServer(logger, nil, newWebSocketHub()).Handle(
		"/unavailable",
		"/unavailable",
		runtimeHandshakeHandler{},
		0,
		0,
		0,
	)
}

type runtimeWebSocketMessage struct {
	kind MessageType
	data []byte
}

type runtimeWebSocketHandler struct {
	connected chan WebSocketConn
	messages  chan runtimeWebSocketMessage
	closed    chan struct{}
}

func newRuntimeWebSocketHandler() *runtimeWebSocketHandler {
	return &runtimeWebSocketHandler{
		connected: make(chan WebSocketConn, 1),
		messages:  make(chan runtimeWebSocketMessage, 1),
		closed:    make(chan struct{}, 1),
	}
}

func (h *runtimeWebSocketHandler) OnConnect(connection WebSocketConn) {
	h.connected <- connection
}

func (h *runtimeWebSocketHandler) OnMessage(
	_ WebSocketConn,
	data []byte,
	kind MessageType,
) {
	h.messages <- runtimeWebSocketMessage{kind: kind, data: append([]byte(nil), data...)}
}

func (h *runtimeWebSocketHandler) OnClose(WebSocketConn) {
	h.closed <- struct{}{}
}

type runtimeHandshakeHandler struct{ err error }

func (h runtimeHandshakeHandler) OnHandshake(*http.Request) error { return h.err }

func receiveRuntimeValue[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(runtimeTestTimeout):
		var zero T
		t.Fatal("timed out waiting for runtime event")
		return zero
	}
}

func assertWebSocketReply(
	t *testing.T,
	client *websocket.Conn,
	wantType int,
	wantData string,
	send func() error,
) {
	t.Helper()
	if err := send(); err != nil {
		t.Fatalf("send reply error = %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(runtimeTestTimeout)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	messageType, data, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	if messageType != wantType || string(data) != wantData {
		t.Fatalf("reply = (%d, %q), want (%d, %q)", messageType, data, wantType, wantData)
	}
}

// stuckWebSocketHandler 的消息回调忽略连接取消，模拟业务处理卡住。
type stuckWebSocketHandler struct {
	received chan struct{}
	release  chan struct{}
}

func (h *stuckWebSocketHandler) OnMessage(WebSocketConn, []byte, MessageType) {
	h.received <- struct{}{}
	<-h.release
}

// WebSocket 关闭与 HTTP Shutdown 并行：卡住的业务回调只拖住 WebSocket 部分，HTTP 立即停止接收新连接、
// 在途请求优雅完成；对端收到 1001 Going Away；Stop 在预算内返回并报告未退出的连接。
func TestHTTPStopDoesNotWaitForStuckWebSocketHandlers(t *testing.T) {
	stuck := &stuckWebSocketHandler{received: make(chan struct{}, 1), release: make(chan struct{})}
	t.Cleanup(func() { close(stuck.release) })
	entered := make(chan struct{})
	spec := NewSpec()
	spec.HTTP().WebSocket("/ws", stuck, Upgrader{CheckOrigin: func(*http.Request) bool { return true }})
	spec.HTTP().Register(HandleHTTP(http.MethodGet, "/slow", func(*http.Request) (any, error) {
		close(entered)
		time.Sleep(300 * time.Millisecond)
		return map[string]string{"ok": "true"}, nil
	}))
	runtime, cleanup, err := NewRuntime(testconfig.New(t, "server", &config_pb.Server{
		Http:      &config_pb.HttpServerOption{Addr: strp("127.0.0.1:0")},
		Grpc:      &config_pb.GrpcServerOption{Disable: boolp(true)},
		StopDelay: durationpb.New(0),
	}), newRuntimeTestLogger(t), testMetricsProvider{registry: prometheus.NewRegistry()},
		runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	runtime.SetReadinessSource(func() bool { return true })
	httpServer, _ := runtime.Servers()
	endpoint, err := httpServer.(transport.Endpointer).Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- httpServer.Start(context.Background()) }()

	peer, _, err := websocket.DefaultDialer.Dial("ws://"+endpoint.Host+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	if err := peer.WriteMessage(websocket.TextMessage, []byte("work")); err != nil {
		t.Fatal(err)
	}
	receiveRuntimeValue(t, stuck.received)
	slow := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + endpoint.Host + "/slow")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				err = errors.New(response.Status)
			}
		}
		slow <- err
	}()
	receiveRuntimeValue(t, entered)

	const budget = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	started := time.Now()
	stopped := make(chan error, 1)
	go func() { stopped <- httpServer.Stop(ctx) }()

	// HTTP Shutdown 不排在 WebSocket 之后：预算远未耗尽时监听已关闭。
	refused := false
	for time.Since(started) < budget/2 {
		connection, err := net.DialTimeout("tcp", endpoint.Host, 50*time.Millisecond)
		if err != nil {
			refused = true
			break
		}
		_ = connection.Close()
		time.Sleep(20 * time.Millisecond)
	}
	if !refused {
		t.Fatal("HTTP listener stayed open while a WebSocket handler was stuck")
	}
	if err := receiveRuntimeValue(t, slow); err != nil {
		t.Fatalf("in-flight HTTP request = %v, want graceful completion", err)
	}
	if err := peer.SetReadDeadline(time.Now().Add(runtimeTestTimeout)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := peer.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseGoingAway) {
		t.Fatalf("peer close = %v, want 1001 going away", err)
	}
	stopErr := receiveRuntimeValue(t, stopped)
	if !errors.Is(stopErr, context.DeadlineExceeded) || !strings.Contains(stopErr.Error(), "websocket connections did not stop") {
		t.Fatalf("Stop = %v, want websocket budget error", stopErr)
	}
	if elapsed := time.Since(started); elapsed > budget+500*time.Millisecond {
		t.Fatalf("Stop took %v, budget %v", elapsed, budget)
	}
	if err := receiveRuntimeValue(t, finished); err != nil {
		t.Fatalf("Start = %v", err)
	}
}
