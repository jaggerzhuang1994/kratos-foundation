package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/middleware"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/gorilla/websocket"
)

func TestWebSocketPreservesMiddlewareContextUntilConnectionCloses(t *testing.T) {
	for _, mode := range []string{"close", "abort", "peer close"} {
		t.Run(mode, func(t *testing.T) {
			handler := &contextWebSocketHandler{handshake: make(chan context.Context, 1), connected: make(chan WebSocketConn, 1), messages: make(chan context.Context, 1), failed: make(chan context.Context, 1), closed: make(chan context.Context, 1)}
			middlewareDone := make(chan struct{})
			propagate := func(next middleware.Handler) middleware.Handler {
				return func(ctx context.Context, req any) (any, error) {
					ctx, cancel := context.WithTimeout(metadata.NewServerContext(ctx, metadata.New(map[string][]string{"x-md-user": {"alice"}})), time.Hour)
					defer close(middlewareDone)
					defer cancel()
					return next(ctx, req)
				}
			}
			hub := newWebSocketHub()
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), runtimeTestTimeout)
				defer cancel()
				if err := hub.stop(ctx); err != nil {
					t.Errorf("stop websocket hub: %v", err)
				}
			})
			server := kratoshttp.NewServer(kratoshttp.Middleware(propagate))
			newWebSocketServer(newRuntimeTestLogger(t), server, hub).Handle("/ws", handler)
			httpServer := httptest.NewServer(server)
			t.Cleanup(httpServer.Close)
			peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws", nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = peer.Close() })
			budget, cancel := context.WithTimeout(t.Context(), runtimeTestTimeout)
			defer cancel()
			select {
			case <-middlewareDone:
			case <-budget.Done():
				t.Fatal("handshake middleware did not return")
			}
			handshake := <-handler.handshake
			assertWebSocketIdentity(t, handshake)
			if _, ok := handshake.Deadline(); !ok {
				t.Error("handshake lost middleware deadline")
			}
			var connection WebSocketConn
			select {
			case connection = <-handler.connected:
			case <-budget.Done():
				t.Fatal("OnConnect not called")
			}
			connectionContext := connection.Request().Context()
			assertWebSocketIdentity(t, connectionContext)
			if err := connectionContext.Err(); err != nil {
				t.Fatalf("handshake return canceled connection: %v", err)
			}
			if _, ok := connectionContext.Deadline(); ok {
				t.Error("connection inherited handshake deadline")
			}
			if err := peer.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
				t.Fatal(err)
			}
			select {
			case ctx := <-handler.messages:
				assertWebSocketIdentity(t, ctx)
				if err := ctx.Err(); err != nil {
					t.Errorf("message context canceled before close: %v", err)
				}
			case <-budget.Done():
				t.Fatal("OnMessage not called")
			}
			switch mode {
			case "close":
				err = connection.Close()
			case "abort":
				err = connection.(*websocketClient).abort()
			case "peer close":
				err = peer.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case ctx := <-handler.failed:
				assertWebSocketIdentity(t, ctx)
			case <-budget.Done():
				t.Fatal("OnError not called")
			}
			select {
			case ctx := <-handler.closed:
				assertWebSocketIdentity(t, ctx)
				if !errors.Is(ctx.Err(), context.Canceled) {
					t.Errorf("closed context error = %v, want canceled", ctx.Err())
				}
			case <-budget.Done():
				t.Fatal("OnClose not called")
			}
			if err := hub.stop(budget); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWebSocketRejectedHandshakePreservesMiddlewareContext(t *testing.T) {
	handler := &contextWebSocketHandler{handshake: make(chan context.Context, 1), handshakeError: errors.New("handshake rejected")}
	middlewareDone := make(chan struct{})
	propagate := func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			ctx, cancel := context.WithCancel(metadata.NewServerContext(ctx, metadata.New(map[string][]string{"x-md-user": {"alice"}})))
			defer close(middlewareDone)
			defer cancel()
			return next(ctx, req)
		}
	}
	hub := newWebSocketHub()
	server := kratoshttp.NewServer(kratoshttp.Middleware(propagate))
	newWebSocketServer(newRuntimeTestLogger(t), server, hub).Handle("/ws", handler)
	server.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ws", nil))
	<-middlewareDone
	ctx := <-handler.handshake
	assertWebSocketIdentity(t, ctx)
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Errorf("rejected handshake context error = %v, want canceled", ctx.Err())
	}
	if err := hub.stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func assertWebSocketIdentity(t *testing.T, ctx context.Context) {
	t.Helper()
	md, _ := metadata.FromServerContext(ctx)
	if got := md.Get("x-md-user"); got != "alice" {
		t.Errorf("middleware identity = %q, want alice", got)
	}
}

type contextWebSocketHandler struct {
	handshake      chan context.Context
	connected      chan WebSocketConn
	messages       chan context.Context
	failed         chan context.Context
	closed         chan context.Context
	handshakeError error
}

func (h *contextWebSocketHandler) OnHandshake(r *http.Request) error {
	h.handshake <- r.Context()
	return h.handshakeError
}

func (h *contextWebSocketHandler) OnConnect(c WebSocketConn) { h.connected <- c }

func (h *contextWebSocketHandler) OnMessage(c WebSocketConn, _ []byte, _ MessageType) {
	h.messages <- c.Request().Context()
}

func (h *contextWebSocketHandler) OnClose(c WebSocketConn) { h.closed <- c.Request().Context() }

func (h *contextWebSocketHandler) OnError(c WebSocketConn, _ error) {
	h.failed <- c.Request().Context()
}

type failedWriteListener struct {
	net.Listener
	fail *atomic.Bool
}

func (l *failedWriteListener) Accept() (net.Conn, error) {
	c, e := l.Listener.Accept()
	if e != nil {
		return nil, e
	}
	return &failedWriteConn{Conn: c, fail: l.fail}, nil
}

type failedWriteConn struct {
	net.Conn
	fail *atomic.Bool
}

func (c *failedWriteConn) Write(p []byte) (int, error) {
	if c.fail.Load() {
		return 0, io.ErrClosedPipe
	}
	return c.Conn.Write(p)
}

func TestWebSocketWriteFailureClosesConnectionAndReleasesReadLoop(t *testing.T) {
	hub := newWebSocketHub()
	handler := newRuntimeWebSocketHandler()
	server := kratoshttp.NewServer()
	newWebSocketServer(newRuntimeTestLogger(t), server, hub).Handle("/ws", handler, Upgrader{CheckOrigin: func(*http.Request) bool { return true }})
	var fail atomic.Bool
	host := httptest.NewUnstartedServer(server)
	host.Listener = &failedWriteListener{Listener: host.Listener, fail: &fail}
	host.Start()
	defer host.Close()
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("close websocket client: %v", err)
		}
	}()
	connection := receiveRuntimeValue(t, handler.connected)
	fail.Store(true)
	if err := connection.SendText("response"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("SendText=%v", err)
	}
	select {
	case <-handler.closed:
	case <-time.After(time.Second):
		t.Error("write failure did not close connection; read loop remains blocked")
		if err := client.Close(); err != nil {
			t.Errorf("close blocked websocket client: %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := hub.stop(ctx); err != nil {
		t.Errorf("stop websocket hub: %v", err)
	}
	hub.mu.Lock()
	remaining := len(hub.clients)
	hub.mu.Unlock()
	if remaining != 0 {
		t.Errorf("failed connection remains in hub: %d", remaining)
	}
}

func TestWebSocketStopDeadlineInterruptsBlockedConnectionClose(t *testing.T) {
	hub := newWebSocketHub()
	handler := newRuntimeWebSocketHandler()
	server := kratoshttp.NewServer()
	newWebSocketServer(newRuntimeTestLogger(t), server, hub).Handle(
		"/ws",
		handler,
		Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
	)
	host := httptest.NewServer(server)
	defer host.Close()

	peer, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(host.URL, "http")+"/ws",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := peer.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("close websocket peer: %v", err)
		}
	}()

	connection := receiveRuntimeValue(t, handler.connected).(*websocketClient)
	connection.writeLock.Lock()
	locked := true
	defer func() {
		if locked {
			connection.writeLock.Unlock()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- hub.stop(ctx) }()

	var stopErr error
	select {
	case stopErr = <-stopped:
	case <-time.After(100 * time.Millisecond):
		connection.writeLock.Unlock()
		locked = false
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("websocket hub remained blocked after releasing write lock")
		}
		t.Fatal("websocket hub ignored its stop context while a connection close was blocked")
	}
	if !errors.Is(stopErr, context.DeadlineExceeded) {
		t.Fatalf("hub.stop error = %v, want context deadline exceeded", stopErr)
	}
	if err := peer.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, _, readErr := peer.ReadMessage()
	if readErr == nil {
		t.Fatal("stop deadline did not force-close the underlying connection")
	}
	var networkError net.Error
	if errors.As(readErr, &networkError) && networkError.Timeout() {
		t.Fatalf("peer only observed its read deadline, not a forced connection close: %v", readErr)
	}

	connection.writeLock.Unlock()
	locked = false
	select {
	case <-handler.closed:
	case <-time.After(time.Second):
		t.Fatal("forced connection close did not release the read loop")
	}
}

func TestWebsocketClientNilAndInvalidMessageBoundaries(t *testing.T) {
	c := &websocketClient{}
	if err := c.Send(MessageType(99), nil); err == nil {
		t.Fatal("unsupported message type accepted")
	}
	if err := c.Send(TextMessage, nil); err == nil {
		t.Fatal("nil connection send succeeded")
	}
	if err := c.SendJSON(func() {}); err == nil {
		t.Fatal("unmarshalable JSON value accepted")
	}
	first := c.Close()
	second := c.Close()
	if first == nil || !errors.Is(second, first) {
		t.Fatalf("close must return a stable nil-connection error: %v / %v", first, second)
	}
}
