package websocket

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/server"
)

type Server = server.WebsocketServer
type Client = server.WebsocketClient

type MessageType = server.MessageType

const TextMessageType = server.TextMessage
const BinaryMessageType = server.BinaryMessage
