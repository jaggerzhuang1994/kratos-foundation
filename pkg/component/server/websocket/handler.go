package websocket

import (
	"net/http"
)

type OnHandshakeHandler interface {
	// OnHandshake 握手前
	OnHandshake(request *http.Request) error
}

type OnConnectHandler interface {
	// OnConnect 建立连接
	OnConnect(client *Client)
}

type OnErrorHandler interface {
	// OnError 读取消息过程发生的错误
	OnError(client *Client, err error)
}

type OnMessageHandler interface {
	// OnMessage 收到消息
	OnMessage(client *Client, message []byte, messageType MessageType)
}

type OnCloseHandler interface {
	// OnClose 关闭连接
	OnClose(client *Client)
}
