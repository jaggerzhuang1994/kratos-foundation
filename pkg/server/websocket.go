package server

import (
	"context"

	log2 "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/gorilla/websocket"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
)

type Upgrader = websocket.Upgrader

type WebsocketServer interface {
	Handle(path string, handler any, optionalUpgrader ...Upgrader)
}

type MessageType int

const (
	TextMessage MessageType = iota + 1
	BinaryMessage
)

type HttpRouter = *http.Router

type OnHandshakeHandler interface {
	// OnHandshake 握手前
	OnHandshake(request *http.Request) error
}

type OnConnectHandler interface {
	// OnConnect 建立连接
	OnConnect(client WebsocketClient)
}

type OnErrorHandler interface {
	// OnError 读取消息过程发生的错误
	OnError(client WebsocketClient, err error)
}

type OnMessageHandler interface {
	// OnMessage 收到消息
	OnMessage(client WebsocketClient, message []byte, messageType MessageType)
}

type OnCloseHandler interface {
	// OnClose 关闭连接
	OnClose(client WebsocketClient)
}

type websocketServer struct {
	log        log.Log
	httpServer HttpServer
	router     HttpRouter
}

func NewWebsocketServer(
	log log.Log,
	httpServer HttpServer,
) WebsocketServer {
	if httpServer == nil {
		return nil
	}
	router := httpServer.Route("/")
	srv := &websocketServer{
		log: log.WithModule("server/websocket").With(
			"client", log2.Valuer(func(ctx context.Context) any {
				request, ok := http.RequestFromServerContext(ctx)
				if ok {
					return request.RemoteAddr
				}
				return ""
			}),
		),
		httpServer: httpServer,
		router:     router,
	}
	return srv
}

func (s *websocketServer) Handle(path string, handler any, optionalUpgrader ...Upgrader) {
	var upgrader = Upgrader{}
	if len(optionalUpgrader) != 0 {
		upgrader = optionalUpgrader[0]
	}
	upgrader.Error = nil

	onHandshakeHandler, _ := handler.(OnHandshakeHandler)
	onConnectHandler, _ := handler.(OnConnectHandler)
	onMessageHandler, _ := handler.(OnMessageHandler)
	onCloseHandler, _ := handler.(OnCloseHandler)
	onErrorHandler, _ := handler.(OnErrorHandler)

	rlog := s.log.With("path", path)
	s.router.Handle("GET", path, func(ctx http.Context) error {
		w := ctx.Response()
		r := ctx.Request()

		http.SetOperation(ctx, path)
		h := ctx.Middleware(func(ctx context.Context, req interface{}) (interface{}, error) {
			clog := rlog.WithContext(ctx)
			request := req.(*http.Request)
			// 新建客户端
			client := newWebsocketClient(clog, request, onHandshakeHandler, onConnectHandler, onMessageHandler, onCloseHandler, onErrorHandler)
			// 建立连接
			err := client.upgrade(upgrader, w)
			if err != nil {
				clog.With("error", err).Warn("websocket upgrade failed")
				return nil, err
			}
			// 处理请求
			go client.resolve()
			return nil, nil
		})
		_, err := h(ctx, r)
		if err != nil {
			return err
		}
		return nil
	})
}
