package kafka

import (
	"fmt"
	"strings"
	"time"
)

// Header 表示一个二进制队列消息 Header。
type Header struct {
	// Key 消息头名称，不允许为空白。
	Key string
	// Value 消息头的二进制值。
	Value []byte
}

// Message 是与驱动无关的队列载荷与传输元数据。
type Message struct {
	// ID 消息标识；受管生产者为空值生成 UUID，消费时缺失则使用 Topic/分区/位点标识。
	ID string
	// Key Kafka 分区路由使用的消息键。
	Key []byte
	// Body 业务消息的二进制载荷。
	Body []byte
	// Headers 传输元数据，可携带追踪上下文。
	Headers []Header
	// Timestamp 消息时间戳；受管生产者在零值时补为当前 UTC 时间。
	Timestamp time.Time
}

// headerCarrier 将消息 Header 暴露给 OpenTelemetry 文本传播器。
type headerCarrier struct {
	// message 承载传播上下文；发送端传入副本供注入，消费端只提取 Header。
	message *Message
}

func (c headerCarrier) Get(key string) string {
	for _, header := range c.message.Headers {
		if header.Key == key {
			return string(header.Value)
		}
	}
	return ""
}

func (c headerCarrier) Set(key string, value string) {
	c.message.Headers = setHeader(c.message.Headers, key, []byte(value))
}

func (c headerCarrier) Keys() []string {
	keys := make([]string, 0, len(c.message.Headers))
	seen := make(map[string]struct{}, len(c.message.Headers))
	for _, header := range c.message.Headers {
		if _, exists := seen[header.Key]; exists {
			continue
		}
		seen[header.Key] = struct{}{}
		keys = append(keys, header.Key)
	}
	return keys
}

// Clone 深度复制消息内所有可变字节切片和 Header。
func (m *Message) Clone() *Message {
	if m == nil {
		return nil
	}
	clone := *m
	clone.Key = append([]byte(nil), m.Key...)
	clone.Body = append([]byte(nil), m.Body...)
	clone.Headers = cloneHeaders(m.Headers)
	return &clone
}

// cloneHeaders 深度复制 Header 及其 Value，切断与调用方的共享底层数组。
func cloneHeaders(headers []Header) []Header {
	cloned := make([]Header, len(headers))
	for index, header := range headers {
		cloned[index] = Header{
			Key:   header.Key,
			Value: append([]byte(nil), header.Value...),
		}
	}
	return cloned
}

// setHeader 将同名 Header 去重为一个新值，并不保留外部 Value 引用。
func setHeader(headers []Header, key string, value []byte) []Header {
	writeIndex := 0
	found := false
	for index := range headers {
		if headers[index].Key == key && !found {
			headers[index].Value = append(headers[index].Value[:0], value...)
			found = true
		} else if headers[index].Key == key {
			// 保留多个系统 Header 会让下游按首个或最后一个读取时得到不同结果，因此在此统一去重。
			continue
		}
		headers[writeIndex] = headers[index]
		writeIndex++
	}
	headers = headers[:writeIndex]
	if found {
		return headers
	}
	return append(headers, Header{Key: key, Value: append([]byte(nil), value...)})
}

// validateHeaders 拒绝空白 Header Key，避免不同后端对该无效值产生不一致解释。
func validateHeaders(headers []Header) error {
	for _, header := range headers {
		if strings.TrimSpace(header.Key) == "" {
			return fmt.Errorf("queue message contains an empty header key")
		}
	}
	return nil
}

// removeInvalidHeaders 仅在死信降级路径移除空白 Key，使原始载荷仍能被隔离保存。
func removeInvalidHeaders(headers []Header) []Header {
	writeIndex := 0
	for _, header := range headers {
		if strings.TrimSpace(header.Key) == "" {
			continue
		}
		headers[writeIndex] = header
		writeIndex++
	}
	return headers[:writeIndex]
}
