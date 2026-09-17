package server

import (
	"context"
	"errors"
	"fmt"
	"sync"

	log2 "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/gorilla/websocket"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// Upgrader 配置 Gorilla WebSocket 协议升级行为。
type Upgrader = websocket.Upgrader

// MessageType 标识 WebSocket 文本帧或二进制帧。
type MessageType int

const (
	// TextMessage 表示 UTF-8 文本帧。
	TextMessage MessageType = iota + 1
	// BinaryMessage 表示二进制帧。
	BinaryMessage
)

type websocketServer struct {
	// log WebSocket 服务日志入口。
	log log.Logger
	// router 注册握手端点的 HTTP 路由器。
	router *http.Router
	// hub 所属运行时共享的连接集合。
	hub *websocketHub
}

// newWebSocketServer 把 WebSocket 路由注册与连接生命周期集中到一个服务对象。
func newWebSocketServer(
	log log.Logger,
	httpServer HTTPServer,
	hub *websocketHub,
) *websocketServer {
	srv := &websocketServer{
		hub: hub,
		log: log.WithModule("server/websocket").With(
			"client", log2.Valuer(func(ctx context.Context) any {
				request, ok := http.RequestFromServerContext(ctx)
				if ok {
					return request.RemoteAddr
				}
				return ""
			}),
		),
	}
	if httpServer != nil {
		srv.router = httpServer.Route("/")
	}
	return srv
}

// Handle 在 HTTP 路由上注册一条 WebSocket 端点。
func (s *websocketServer) Handle(path string, handler any, maxMessageBytes int64, optionalUpgrader ...Upgrader) {
	if s.router == nil {
		s.log.Warn("failed to handle websocket path: HTTP server is not initialized")
		return
	}

	var upgrader = Upgrader{}
	if len(optionalUpgrader) != 0 {
		upgrader = optionalUpgrader[0]
	}

	rlog := s.log.With("path", path)
	s.router.Handle("GET", path, func(ctx http.Context) error {
		w := ctx.Response()
		r := ctx.Request()

		http.SetOperation(ctx, path)
		h := ctx.Middleware(func(ctx context.Context, req any) (any, error) {
			clog := rlog.WithContext(ctx)
			request, ok := req.(*http.Request)
			if !ok || request == nil {
				return nil, fmt.Errorf("websocket middleware request has type %T, want *http.Request", req)
			}
			// 握手需要保留中间件派生的身份、metadata 和请求截止时间。
			client, err := upgrade(upgrader, clog, request.WithContext(ctx), w, handler, maxMessageBytes)
			if err != nil {
				clog.With("error", err).Warn("websocket upgrade failed")
				return nil, err
			}
			if !s.hub.serve(client) {
				if closeErr := client.Close(); closeErr != nil {
					clog.With("error", closeErr).Warn("websocket connection close failed")
				}
				clog.Warn("websocket server is stopping")
			}
			return nil, nil
		})
		_, err := h(ctx, r)
		if err != nil {
			return err
		}
		return nil
	})
}

type websocketHub struct {
	// mu 保护连接集合与停机状态。
	mu sync.Mutex
	// clients 尚未退出的 WebSocket 连接。
	clients map[*websocketClient]struct{}
	// stopping 是否已开始停机，阻止登记新连接。
	stopping bool
	// done 停机后连接全部退出的完成信号。
	done chan struct{}
	// doneOnce 确保完成信号只关闭一次。
	doneOnce sync.Once
}

// newWebSocketHub 创建独立连接集合，确保不同 Runtime 之间没有全局状态。
func newWebSocketHub() *websocketHub {
	return &websocketHub{
		clients: make(map[*websocketClient]struct{}),
		done:    make(chan struct{}),
	}
}

// serve 在停止开始前登记连接，并由单独协程负责读取及最终移除。
func (h *websocketHub) serve(client *websocketClient) bool {
	h.mu.Lock()
	if h.stopping {
		h.mu.Unlock()
		return false
	}
	h.clients[client] = struct{}{}
	h.mu.Unlock()
	go func() {
		defer func() {
			h.mu.Lock()
			delete(h.clients, client)
			done := h.stopping && len(h.clients) == 0
			h.mu.Unlock()
			if done {
				h.doneOnce.Do(func() {
					close(h.done)
				})
			}
		}()
		client.resolve()
	}()
	return true
}

// stop 拒绝新连接、主动关闭现有连接并等待所有读循环退出。
func (h *websocketHub) stop(ctx context.Context) error {
	h.mu.Lock()
	h.stopping = true
	clients := make([]*websocketClient, 0, len(h.clients))
	for client := range h.clients {
		clients = append(clients, client)
	}
	done := len(clients) == 0
	h.mu.Unlock()
	if done {
		h.doneOnce.Do(func() {
			close(h.done)
		})
	}
	closeResults := make(chan error, len(clients))
	for _, client := range clients {
		go func() {
			closeResults <- client.Close()
		}()
	}

	var closeErrors []error
	remaining := len(clients)
	doneChannel := h.done
	for remaining > 0 || doneChannel != nil {
		select {
		case err := <-closeResults:
			closeErrors = append(closeErrors, err)
			remaining--
		case <-doneChannel:
			doneChannel = nil
		case <-ctx.Done():
			// 正常关闭可能正在等待某个写操作；预算耗尽后直接关闭 socket，打断
			// Gorilla 读写并让既有读循环继续执行 closeOnce、OnClose 和 hub 移除。
			for _, client := range clients {
				closeErrors = append(closeErrors, client.abort())
			}
			return errors.Join(
				errors.Join(closeErrors...),
				ctx.Err(),
				errors.New("websocket connections did not stop"),
			)
		}
	}
	return errors.Join(closeErrors...)
}

// OnHandshakeHandler 可在 HTTP 连接升级前拒绝请求。
type OnHandshakeHandler interface {
	OnHandshake(request *http.Request) error
}

// OnConnectHandler 在 WebSocket 连接接受后调用。
type OnConnectHandler interface {
	OnConnect(client WebSocketConn)
}

// OnErrorHandler 接收导致读循环结束的连接错误。
type OnErrorHandler interface {
	OnError(client WebSocketConn, err error)
}

// OnMessageHandler 接收每一帧完整的文本或二进制消息。
type OnMessageHandler interface {
	OnMessage(client WebSocketConn, message []byte, messageType MessageType)
}

// OnCloseHandler 在连接读循环退出后调用一次。
type OnCloseHandler interface {
	OnClose(client WebSocketConn)
}
