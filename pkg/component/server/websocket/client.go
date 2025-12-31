package websocket

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
)

const writeWait = time.Second

type Client struct {
	log     *log.Log
	conn    *websocket.Conn
	Request *http.Request

	writeLock sync.Mutex

	closeOnce sync.Once

	onConnectHandler OnConnectHandler
	onMessageHandler OnMessageHandler
	onCloseHandler   OnCloseHandler
	onErrorHandler   OnErrorHandler
}

func (c *Client) resolve(_ context.Context) {
	defer func() {
		c.conn.Close()
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
						buf := make([]byte, 64<<10) //nolint:mnd
						n := runtime.Stack(buf, false)
						buf = buf[:n]
						c.log.Errorf("onMessageHandler panic: %v%s\n", r, buf)
					}
				}()
				c.onMessageHandler.OnMessage(c, m, MessageType(mt))
			}(mt, m)
		}
	}
}

func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.writeLock.Lock()
		defer c.writeLock.Unlock()
		_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(writeWait))
	})
}

func (c *Client) Send(messageType MessageType, data []byte) error {
	c.writeLock.Lock()
	defer c.writeLock.Unlock()
	// _ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return c.conn.WriteMessage(int(messageType), data)
}

func (c *Client) SendJSON(data any) error {
	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return c.SendText(string(bytes))
}

func (c *Client) SendText(data string) error {
	return c.Send(TextMessage, []byte(data))
}

func (c *Client) SendBinary(data []byte) error {
	return c.Send(BinaryMessage, data)
}
