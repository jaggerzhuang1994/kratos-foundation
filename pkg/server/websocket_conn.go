package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// WebSocketConn 表示服务端接受的一条 WebSocket 连接；Close 幂等，OnClose 后不得继续持有。
type WebSocketConn interface {
	// Request 返回升级请求及中间件上下文值；成功握手后 Context 由连接关闭取消。
	Request() *http.Request
	// Close 发起正常关闭并释放底层连接。
	Close() error
	// Send 写入一帧指定类型的消息。
	Send(MessageType, []byte) error
	// SendJSON 把数据序列化为 JSON 文本帧。
	SendJSON(any) error
	// SendText 写入一帧文本消息。
	SendText(string) error
	// SendBinary 写入一帧二进制消息。
	SendBinary([]byte) error
}

const writeWait = time.Second

// 默认限制整条接收消息；缓冲区大小不等于消息大小上限。
const defaultWebSocketMaxMessageBytes int64 = 1 << 20

type websocketClient struct {
	log.Logger

	request         *http.Request
	conn            *websocket.Conn
	maxMessageBytes int64
	cancel          context.CancelFunc

	onConnectHandler OnConnectHandler
	onMessageHandler OnMessageHandler
	onCloseHandler   OnCloseHandler
	onErrorHandler   OnErrorHandler

	writeLock sync.Mutex
	closeOnce sync.Once
	closeErr  error
}

// upgrade 依次执行应用握手校验和协议升级，再解析处理器支持的事件接口。
func upgrade(
	upgrader Upgrader,
	log log.Logger,
	request *http.Request,
	w http.ResponseWriter,
	handler any,
	maxMessageBytes int64,
) (client *websocketClient, err error) {
	onHandshakeHandler, _ := handler.(OnHandshakeHandler)
	onConnectHandler, _ := handler.(OnConnectHandler)
	onMessageHandler, _ := handler.(OnMessageHandler)
	onCloseHandler, _ := handler.(OnCloseHandler)
	onErrorHandler, _ := handler.(OnErrorHandler)

	if onHandshakeHandler != nil {
		err = onHandshakeHandler.OnHandshake(request)
		if err != nil {
			return nil, fmt.Errorf("websocket handshake: %w", err)
		}
	}

	conn, err := upgrader.Upgrade(w, request, w.Header())
	if err != nil {
		return nil, fmt.Errorf("upgrade websocket connection: %w", err)
	}

	if maxMessageBytes == 0 {
		maxMessageBytes = defaultWebSocketMaxMessageBytes
	}
	// Gorilla 限制线上载荷；readMessage 另限制解压后字节，防止协商压缩绕过上限。
	conn.SetReadLimit(maxMessageBytes)
	// HTTP 请求及握手中间件返回时会取消原上下文。连接保留其值，并独立拥有
	// 取消生命周期；仅在升级成功后创建，失败路径没有待释放的连接上下文。
	ctx, cancel := context.WithCancel(context.WithoutCancel(request.Context()))
	client = &websocketClient{
		Logger:           log,
		request:          request.WithContext(ctx),
		conn:             conn,
		maxMessageBytes:  maxMessageBytes,
		cancel:           cancel,
		onConnectHandler: onConnectHandler,
		onMessageHandler: onMessageHandler,
		onCloseHandler:   onCloseHandler,
		onErrorHandler:   onErrorHandler,
	}

	return client, nil
}

// readMessage 对压缩后的传输载荷与解压结果分别限流，超限数据不进入业务回调。
func (c *websocketClient) readMessage() (int, []byte, error) {
	if c.maxMessageBytes <= 0 {
		return c.conn.ReadMessage()
	}
	messageType, reader, err := c.conn.NextReader()
	if err != nil {
		return messageType, nil, err
	}
	data, err := io.ReadAll(io.LimitReader(reader, c.maxMessageBytes))
	if err != nil {
		return messageType, nil, err
	}
	if int64(len(data)) < c.maxMessageBytes {
		return messageType, data, nil
	}
	// 额外探测一字节区分恰好到达上限与超限，不计算 max+1，避免整数溢出。
	var extra [1]byte
	n, err := io.ReadFull(reader, extra[:])
	if n > 0 {
		closeErr := c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseMessageTooBig, "message too large"), time.Now().Add(writeWait))
		return messageType, nil, errors.Join(websocket.ErrReadLimit, closeErr)
	}
	if err != io.EOF {
		return messageType, nil, err
	}
	return messageType, data, nil
}

// resolve 持续读取完整消息，并把连接事件隔离到对应处理器。
func (c *websocketClient) resolve() {
	defer func() {
		if r := recover(); r != nil {
			if c.Logger != nil {
				c.Errorf("resolve panic: %v\n%s", r, debug.Stack())
			}
		}
	}()
	defer func() {
		if err := c.close(false); err != nil {
			if c.Logger != nil {
				c.With("error", err).Warn("websocket connection close failed")
			}
		}
		if c.onCloseHandler != nil {
			c.onCloseHandler.OnClose(c)
		}
	}()
	if c.onConnectHandler != nil {
		c.onConnectHandler.OnConnect(c)
	}
	for {
		mt, m, err := c.readMessage()
		if err != nil {
			if errors.Is(err, websocket.ErrReadLimit) && c.Logger != nil {
				c.With("error", err, "max_message_bytes", c.maxMessageBytes).Warn("readMessage | websocket message too large")
			}
			if c.onErrorHandler != nil {
				c.onErrorHandler.OnError(c, err)
			}
			break
		}
		if c.onMessageHandler != nil {
			func() {
				defer func() {
					if r := recover(); r != nil {
						if c.Logger != nil {
							c.Errorf("onMessageHandler panic: %v\n%s", r, debug.Stack())
						}
					}
				}()
				c.onMessageHandler.OnMessage(c, m, MessageType(mt))
			}()
		}
	}
}

// Request 返回建立当前连接时的升级请求。
func (c *websocketClient) Request() *http.Request {
	return c.request
}

// Close 幂等发送正常关闭帧并释放底层连接。
func (c *websocketClient) Close() error {
	return c.close(true)
}

// close 让读循环退出与主动关闭共用一次资源释放，同时只在主动关闭时发送控制帧。
func (c *websocketClient) close(sendControl bool) error {
	c.closeOnce.Do(func() {
		if c.conn == nil {
			c.closeErr = errors.New("websocket connection is nil")
			return
		}
		// 有效连接在 upgrade 中持有取消函数；先通知业务退出，再等待已有写操作。
		c.cancel()
		c.writeLock.Lock()
		defer c.writeLock.Unlock()
		var closeErrors []error
		if sendControl {
			closeErrors = append(closeErrors, c.conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
				time.Now().Add(writeWait),
			))
		}
		closeErrors = append(closeErrors, c.conn.Close())
		c.closeErr = errors.Join(closeErrors...)
	})
	return c.closeErr
}

// abort 直接关闭底层连接，用于停机预算耗尽时打断正在进行的读写。读循环仍负责
// 执行统一的 closeOnce 和 OnClose；net.Conn.Close 可与 Gorilla 的读写并发调用。
func (c *websocketClient) abort() error {
	c.cancel()
	return c.conn.Close()
}

// Send 校验消息类型并在单写锁内设置截止时间和写入完整帧。
func (c *websocketClient) Send(messageType MessageType, data []byte) error {
	if messageType != TextMessage && messageType != BinaryMessage {
		return fmt.Errorf("unsupported websocket message type %d", messageType)
	}
	if c.conn == nil {
		return errors.New("websocket connection is nil")
	}
	err := func() error {
		c.writeLock.Lock()
		defer c.writeLock.Unlock()
		if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
			return fmt.Errorf("set websocket write deadline: %w", err)
		}
		return c.conn.WriteMessage(int(messageType), data)
	}()
	if err != nil {
		// Gorilla 写失败后的连接不可复用。先释放写锁再关底层连接，使阻塞的读
		// 循环退出并执行既有 OnClose/hub 清理；不向已损坏的流追加关闭帧。
		return errors.Join(err, c.close(false))
	}
	return nil
}

// SendText 写入一帧 UTF-8 文本消息。
func (c *websocketClient) SendText(data string) error {
	return c.Send(TextMessage, []byte(data))
}

// SendBinary 写入一帧二进制消息。
func (c *websocketClient) SendBinary(data []byte) error {
	return c.Send(BinaryMessage, data)
}

// SendJSON 先完成序列化再加写锁，避免 CPU 编码阻塞其他连接写操作。
func (c *websocketClient) SendJSON(data any) error {
	bytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal websocket JSON message: %w", err)
	}
	return c.SendText(string(bytes))
}
