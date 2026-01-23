// Package websocket 提供 WebSocket 服务器和客户端的类型别名
// 通过类型别名简化对 pkg/server 包中 WebSocket 相关类型的引用
package websocket

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/server"
)

// Server 是 WebSocket 服务器的类型别名
// 封装了 gorilla/websocket 的服务器实现，提供连接管理、消息处理等功能
type Server = server.WebsocketServer

// Client 是 WebSocket 客户端的类型别名
// 封装了 gorilla/websocket 的客户端实现，提供连接、发送、接收等功能
type Client = server.WebsocketClient

// MessageType 是消息类型的类型别名
// 用于区分不同类型的 WebSocket 消息（文本或二进制）
type MessageType = server.MessageType

// TextMessageType 表示文本消息类型
// 用于发送和接收 UTF-8 编码的文本数据
const TextMessageType = server.TextMessage

// BinaryMessageType 表示二进制消息类型
// 用于发送和接收二进制数据（如图片、视频等）
const BinaryMessageType = server.BinaryMessage
