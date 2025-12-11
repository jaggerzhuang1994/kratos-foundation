package websocket

import (
	"context"
	"runtime"

	log2 "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/gorilla/websocket"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
)

type Upgrader = websocket.Upgrader

type MessageType int

const (
	TextMessage MessageType = iota + 1
	BinaryMessage
)

type Server struct {
	log        *log.Log
	httpServer *http.Server
	router     *http.Router
}

func NewServer(
	log *log.Log,
	httpServer *http.Server,
) *Server {
	router := httpServer.Route("/")
	return &Server{
		log: log.WithModule("websocket").With(
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
}

func (s *Server) Handle(path string, handler any, optionalUpgrader ...Upgrader) {
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

	log := s.log.With("path", path)

	s.router.Handle("GET", path, func(ctx http.Context) error {
		var w = ctx.Response()
		var r = ctx.Request()

		http.SetOperation(ctx, path)
		h := ctx.Middleware(func(ctx context.Context, req interface{}) (interface{}, error) {
			var err error
			request := req.(*http.Request)
			log_ := log.WithContext(ctx)

			log_.Debug("connecting")
			// 握手前的校验
			if onHandshakeHandler != nil {
				err = onHandshakeHandler.OnHandshake(request)
				if err != nil {
					log_.Debug("on_handshake failure: ", err)
					return nil, err
				}
			}
			// 建立ws链接
			conn, err := upgrader.Upgrade(w, request, w.Header())
			if err != nil {
				log_.Debug("upgrade failure: ", err)
				return nil, err
			}
			log_.Debug("connected")

			go func() {
				cctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
				defer cancel()
				defer func() {
					if r := recover(); r != nil {
						buf := make([]byte, 64<<10) //nolint:mnd
						n := runtime.Stack(buf, false)
						buf = buf[:n]
						log_.Errorf("client.resolve panic: %v%s\n", r, buf)
					}
				}()
				client := &Client{
					log:              log_,
					conn:             conn,
					onConnectHandler: onConnectHandler,
					onMessageHandler: onMessageHandler,
					onCloseHandler:   onCloseHandler,
					onErrorHandler:   onErrorHandler,
					Request:          request,
				}
				client.resolve(cctx)
			}()
			return nil, nil
		})
		_, err := h(ctx, r)
		if err != nil {
			return err
		}
		return nil
	})
}
