// Package server 提供 WebSocket 服务器的功能
//
// 该文件定义了 WebSocket 服务器的接口和实现，基于 gorilla/websocket
// 提供了完整的 WebSocket 连接管理功能。
//
// 主要功能：
//   - WebSocket 升级处理
//   - 连接生命周期管理
//   - 消息处理（文本和二进制）
//   - 错误处理和日志记录
//
// 使用方式：
//
//	// 定义处理器
//	type MyHandler struct{}
//
//	func (h *MyHandler) OnConnect(client websocket.Client) {
//	    log.Println("Client connected:", client.ID())
//	}
//
//	func (h *MyHandler) OnMessage(client websocket.Client, data []byte, messageType websocket.MessageType) {
//	    // 处理消息
//	}
//
//	func (h *MyHandler) OnClose(client websocket.Client) {
//	    log.Println("Client disconnected:", client.ID())
//	}
//
//	// 注册路由
//	wsServer.Handle("/ws", &MyHandler{})
package server

import (
	"context"

	log2 "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/gorilla/websocket"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
)

// Upgrader WebSocket 升级器的类型别名
//
// 该类型是 gorilla/websocket 的 HTTP 升级器，用于将 HTTP 连接
// 升级为 WebSocket 连接
type Upgrader = websocket.Upgrader

// HttpRouter HTTP 路由器的类型别名
//
// 该类型是 Kratos HTTP 服务器的路由器，用于注册 WebSocket 路由
type HttpRouter = *http.Router

// WebsocketServer WebSocket 服务器接口
//
// 该接口定义了 WebSocket 路由的注册方法，用于将 WebSocket
// 处理器绑定到指定的路径。
//
// 使用方式：
//
//	wsServer.Handle("/chat", &ChatHandler{})
//	wsServer.Handle("/notifications", &NotificationHandler{},
//	    websocket.Upgrader{HandshakeTimeout: time.Second * 10})
type WebsocketServer interface {
	Handle(path string, handler any, optionalUpgrader ...Upgrader)
}

// MessageType WebSocket 消息类型
//
// 该类型定义了 WebSocket 支持的消息类型：
//   - TextMessage: 文本消息（UTF-8 编码）
//   - BinaryMessage: 二进制消息（如图片、视频等）
type MessageType int

const (
	// TextMessage 文本消息类型
	// 用于发送和接收 UTF-8 编码的文本数据
	TextMessage MessageType = iota + 1

	// BinaryMessage 二进制消息类型
	// 用于发送和接收二进制数据（如图片、视频、protobuf 等）
	BinaryMessage
)

// websocketServer WebSocket 服务器的实现
//
// 该结构体管理 WebSocket 连接的路由和处理，集成了日志记录功能。
type websocketServer struct {
	log    log.Log    // 日志记录器
	router HttpRouter // HTTP 路由器，用于注册 WebSocket 路由
}

// NewWebsocketServer 创建 WebSocket 服务器
//
// 该函数创建一个 WebSocket 服务器实例，并配置日志记录器。
// 日志记录器会自动记录客户端地址等上下文信息。
//
// 参数说明：
//   - _: Setup 接口（未使用，仅用于确保依赖注入顺序）
//   - log: 日志记录器
//   - httpServer: HTTP 服务器实例，用于获取路由器
//
// 返回：
//   - WebsocketServer: WebSocket 服务器实例
//
// 注意事项：
//   - 如果 httpServer 为 nil，Handle 方法会记录警告并返回
//   - 日志记录器会自动添加客户端地址信息
func NewWebsocketServer(
	_ Setup, // Setup 接口（确保在服务器创建之前执行）
	log log.Log, // 日志记录器
	httpServer HttpServer, // HTTP 服务器实例
) WebsocketServer {
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
	}
	if httpServer != nil {
		srv.router = httpServer.Route("/")
	}
	return srv
}

// Handle 注册 WebSocket 处理器
//
// 该方法将 WebSocket 处理器注册到指定的路径，并设置连接升级、
// 消息处理等逻辑。
//
// 参数说明：
//   - path: WebSocket 路由路径（如 "/ws"、"/chat"）
//   - handler: WebSocket 处理器，可以实现以下接口：
//   - OnHandshakeHandler: 握手前回调
//   - OnConnectHandler: 连接建立回调
//   - OnMessageHandler: 消息接收回调
//   - OnCloseHandler: 连接关闭回调
//   - OnErrorHandler: 错误处理回调
//   - optionalUpgrader: 可选的升级器配置，如果不提供则使用默认配置
//
// 处理流程：
//  1. 检查 HTTP 服务器是否已初始化
//  2. 使用 Upgrader 将 HTTP 连接升级为 WebSocket 连接
//  3. 创建 WebSocket 客户端实例
//  4. 在独立的 goroutine 中处理消息循环
//
// 注意事项：
//   - 如果 HTTP 服务器未初始化，会记录警告并返回
//   - 每个连接在独立的 goroutine 中处理
//   - 错误会被自动捕获并记录
func (s *websocketServer) Handle(path string, handler any, optionalUpgrader ...Upgrader) {
	if s.router == nil {
		s.log.Warn("failed to handle websocket path: HTTP server is not initialized")
		return
	}

	// 配置升级器
	var upgrader = Upgrader{}
	if len(optionalUpgrader) != 0 {
		upgrader = optionalUpgrader[0]
	}
	upgrader.Error = nil // 使用默认的错误处理

	// 提取处理器接口
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
			// 处理请求（在独立的 goroutine 中运行）
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

// OnHandshakeHandler 握手前回调接口
//
// 该接口定义了 WebSocket 握手前的回调方法，用于：
//   - 验证请求参数
//   - 检查认证信息
//   - 自定义握手逻辑
//
// 使用示例：
//
//	func (h *MyHandler) OnHandshake(request *http.Request) error {
//	    token := request.URL.Query().Get("token")
//	    if !validateToken(token) {
//	        return errors.New("invalid token")
//	    }
//	    return nil
//	}
type OnHandshakeHandler interface {
	// OnHandshake 握手前回调
	//
	// 该方法在 WebSocket 升级之前调用，可以验证请求参数。
	//
	// 参数：
	//   - request: HTTP 请求对象
	//
	// 返回：
	//   - error: 如果返回错误，握手失败
	OnHandshake(request *http.Request) error
}

// OnConnectHandler 连接建立回调接口
//
// 该接口定义了 WebSocket 连接建立后的回调方法，用于：
//   - 记录连接日志
//   - 初始化连接状态
//   - 发送欢迎消息
//
// 使用示例：
//
//	func (h *MyHandler) OnConnect(client websocket.Client) {
//	    log.Printf("Client %s connected", client.ID())
//	    client.Send([]byte("Welcome!"))
//	}
type OnConnectHandler interface {
	// OnConnect 连接建立回调
	//
	// 该方法在 WebSocket 连接建立成功后调用。
	//
	// 参数：
	//   - client: WebSocket 客户端实例
	OnConnect(client WebsocketClient)
}

// OnErrorHandler 错误处理回调接口
//
// 该接口定义了读取消息过程中发生错误时的回调方法，用于：
//   - 记录错误日志
//   - 处理连接异常
//   - 清理资源
//
// 使用示例：
//
//	func (h *MyHandler) OnError(client websocket.Client, err error) {
//	    log.Printf("Client %s error: %v", client.ID(), err)
//	}
type OnErrorHandler interface {
	// OnError 错误处理回调
	//
	// 该方法在读取消息过程中发生错误时调用。
	//
	// 参数：
	//   - client: WebSocket 客户端实例
	//   - err: 错误信息
	OnError(client WebsocketClient, err error)
}

// OnMessageHandler 消息接收回调接口
//
// 该接口定义了接收到消息时的回调方法，用于：
//   - 处理业务逻辑
//   - 广播消息
//   - 响应客户端
//
// 使用示例：
//
//	func (h *MyHandler) OnMessage(client websocket.Client, data []byte, messageType websocket.MessageType) {
//	    log.Printf("Received from %s: %s", client.ID(), string(data))
//	    // 处理消息...
//	}
type OnMessageHandler interface {
	// OnMessage 消息接收回调
	//
	// 该方法在接收到客户端消息时调用。
	//
	// 参数：
	//   - client: WebSocket 客户端实例
	//   - message: 消息内容
	//   - messageType: 消息类型（文本或二进制）
	OnMessage(client WebsocketClient, message []byte, messageType MessageType)
}

// OnCloseHandler 连接关闭回调接口
//
// 该接口定义了 WebSocket 连接关闭时的回调方法，用于：
//   - 记录关闭日志
//   - 清理连接资源
//   - 通知其他客户端
//
// 使用示例：
//
//	func (h *MyHandler) OnClose(client websocket.Client) {
//	    log.Printf("Client %s disconnected", client.ID())
//	    // 清理资源...
//	}
type OnCloseHandler interface {
	// OnClose 连接关闭回调
	//
	// 该方法在 WebSocket 连接关闭时调用。
	//
	// 参数：
	//   - client: WebSocket 客户端实例
	OnClose(client WebsocketClient)
}
