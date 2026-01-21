package websocket

import (
	"github.com/gorilla/websocket"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/server"
)

type Server = server.WebsocketServer
type Client = server.WebsocketClient
type Upgrader = websocket.Upgrader
type CloseError = websocket.CloseError
type MessageType = server.MessageType

const TextMessage = server.TextMessage
const BinaryMessage = server.BinaryMessage

var ErrBadHandshake = websocket.ErrBadHandshake
var ErrCloseSent = websocket.ErrCloseSent
var ErrReadLimit = websocket.ErrReadLimit

var IsCloseError = websocket.IsCloseError
var IsUnexpectedCloseError = websocket.IsUnexpectedCloseError
