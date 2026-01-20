package server

import (
	"encoding/json"
	"io"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
	"github.com/pkg/errors"
)

type WebsocketClient interface {
	Request() *http.Request
	Close()
	Send(MessageType, []byte) error
	SendJSON(any) error
	SendText(string) error
	SendBinary([]byte) error
}

const writeWait = time.Second

type websocketClient struct {
	log.Log

	onHandshakeHandler OnHandshakeHandler
	onConnectHandler   OnConnectHandler
	onMessageHandler   OnMessageHandler
	onCloseHandler     OnCloseHandler
	onErrorHandler     OnErrorHandler

	writeLock sync.Mutex
	closeOnce sync.Once

	request *http.Request
	conn    *websocket.Conn
}

func newWebsocketClient(
	log log.Log,
	request *http.Request,
	onHandshakeHandler OnHandshakeHandler,
	onConnectHandler OnConnectHandler,
	onMessageHandler OnMessageHandler,
	onCloseHandler OnCloseHandler,
	onErrorHandler OnErrorHandler,
) *websocketClient {
	return &websocketClient{
		Log:                log,
		onHandshakeHandler: onHandshakeHandler,
		onConnectHandler:   onConnectHandler,
		onMessageHandler:   onMessageHandler,
		onCloseHandler:     onCloseHandler,
		onErrorHandler:     onErrorHandler,
		request:            request,
	}
}

// 建立 ws 连接
func (c *websocketClient) upgrade(
	upgrader Upgrader,
	w http.ResponseWriter,
) error {
	var err error

	// 自定义握手处理
	if c.onHandshakeHandler != nil {
		err = c.onHandshakeHandler.OnHandshake(c.request)
		if err != nil {
			return errors.WithMessage(err, "OnHandshake failure")
		}
	}

	// 建立 ws 链接
	conn, err := upgrader.Upgrade(w, c.request, w.Header())
	if err != nil {
		return errors.WithMessage(err, "Upgrade failure")
	}

	c.conn = conn
	return nil
}

func (c *websocketClient) resolve() {
	defer func() {
		if r := recover(); r != nil {
			c.Errorf("resolve panic: %v\n%s", r, debug.Stack())
		}
	}()
	defer func() {
		_ = c.conn.Close()
		if c.onCloseHandler != nil {
			c.onCloseHandler.OnClose(c)
		}
	}()
	if c.onConnectHandler != nil {
		c.onConnectHandler.OnConnect(c)
	}
	for {
		mt, m, err := c.conn.ReadMessage()
		if err != nil {
			if c.onErrorHandler != nil {
				c.onErrorHandler.OnError(c, err)
			}
			if err == io.EOF || websocket.IsUnexpectedCloseError(err) {
				break
			}
			continue
		}
		if c.onMessageHandler != nil {
			go func(mt int, m []byte) {
				defer func() {
					if r := recover(); r != nil {
						c.Errorf("onMessageHandler panic: %v\n%s", r, debug.Stack())
					}
				}()
				c.onMessageHandler.OnMessage(c, m, MessageType(mt))
			}(mt, m)
		}
	}
}

func (c *websocketClient) Request() *http.Request {
	return c.request
}

func (c *websocketClient) Close() {
	c.closeOnce.Do(func() {
		c.writeLock.Lock()
		defer c.writeLock.Unlock()
		_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(writeWait))
	})
}

func (c *websocketClient) Send(messageType MessageType, data []byte) error {
	c.writeLock.Lock()
	defer c.writeLock.Unlock()
	// _ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return c.conn.WriteMessage(int(messageType), data)
}

func (c *websocketClient) SendText(data string) error {
	return c.Send(TextMessage, []byte(data))
}

func (c *websocketClient) SendBinary(data []byte) error {
	return c.Send(BinaryMessage, data)
}

func (c *websocketClient) SendJSON(data any) error {
	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return c.SendText(string(bytes))
}
