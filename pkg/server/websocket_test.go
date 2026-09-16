package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/gorilla/websocket"
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
		handler,
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
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("upgrade() error = %v, want %v", err, wantErr)
	}
	newWebSocketServer(logger, nil, newWebSocketHub()).Handle(
		"/unavailable",
		runtimeHandshakeHandler{},
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
