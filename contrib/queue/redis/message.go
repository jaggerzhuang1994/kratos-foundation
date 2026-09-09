package redis

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	goredis "github.com/redis/go-redis/v9"
)

const (
	fieldID        = "id"
	fieldKey       = "key"
	fieldBody      = "body"
	fieldHeaders   = "headers"
	fieldTimestamp = "timestamp"
)

// newXAddArgs 将通用消息编码为 Redis XADD 参数。
func newXAddArgs(
	config ProducerConfig,
	message *queue.Message,
) *goredis.XAddArgs {
	// Header 只含 string 和 []byte，json.Marshal 对这两种类型没有可失败分支。
	headers, _ := json.Marshal(message.Headers)
	args := &goredis.XAddArgs{
		Stream: config.Stream,
		Values: map[string]any{
			fieldID:        message.ID,
			fieldKey:       append([]byte(nil), message.Key...),
			fieldBody:      append([]byte(nil), message.Body...),
			fieldHeaders:   headers,
			fieldTimestamp: strconv.FormatInt(message.Timestamp.UnixNano(), 10),
		},
	}
	if config.MaxLength > 0 {
		args.MaxLen = config.MaxLength
		args.Approx = config.ApproximateMaxLength
	}
	return args
}

// malformedMessage 从解码失败的 Redis 记录中尽可能保留死信调查所需数据。
func malformedMessage(raw goredis.XMessage) *queue.Message {
	message := &queue.Message{ID: raw.ID}
	if id, err := valueString(raw.Values[fieldID]); err == nil && id != "" {
		message.ID = id
	}
	// 此路径本就在保存解码失败的原始投递，单个字段不可读时优先保留其他可用证据。
	message.Key, _ = valueBytes(raw.Values[fieldKey])
	message.Body, _ = valueBytes(raw.Values[fieldBody])
	if len(message.Body) == 0 {
		encoded, err := json.Marshal(raw.Values)
		if err != nil {
			// Redis 正常只返回字符串或字节；自定义 Hook 违反该契约时仍留下可诊断文本。
			message.Body = []byte(fmt.Sprintf("%v", raw.Values))
		} else {
			message.Body = encoded
		}
	}
	return message
}

// decodeMessage 将 Redis Stream 字段严格解码为通用队列消息。
func decodeMessage(raw goredis.XMessage) (*queue.Message, error) {
	id, err := valueString(raw.Values[fieldID])
	if err != nil {
		return nil, fmt.Errorf("id: %w", err)
	}
	if id == "" {
		id = raw.ID
	}
	key, err := valueBytes(raw.Values[fieldKey])
	if err != nil {
		return nil, fmt.Errorf("key: %w", err)
	}
	body, err := valueBytes(raw.Values[fieldBody])
	if err != nil {
		return nil, fmt.Errorf("body: %w", err)
	}
	headersValue, err := valueBytes(raw.Values[fieldHeaders])
	if err != nil {
		return nil, fmt.Errorf("headers: %w", err)
	}
	var headers []queue.Header
	if len(headersValue) > 0 {
		if err := json.Unmarshal(headersValue, &headers); err != nil {
			return nil, fmt.Errorf("headers: %w", err)
		}
	}
	timestampValue, err := valueString(raw.Values[fieldTimestamp])
	if err != nil {
		return nil, fmt.Errorf("timestamp: %w", err)
	}
	var timestamp time.Time
	if timestampValue != "" {
		nanoseconds, err := strconv.ParseInt(timestampValue, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("timestamp: %w", err)
		}
		timestamp = time.Unix(0, nanoseconds).UTC()
	}
	return &queue.Message{
		ID:        id,
		Key:       key,
		Body:      body,
		Headers:   headers,
		Timestamp: timestamp,
	}, nil
}

// valueBytes 只接受 go-redis 可能返回的字符串和字节值，并复制字节所有权。
func valueBytes(value any) ([]byte, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		return []byte(typed), nil
	case []byte:
		return append([]byte(nil), typed...), nil
	default:
		return nil, fmt.Errorf("unsupported Redis value type %T", value)
	}
}

// valueString 在 valueBytes 严格校验后返回字符串。
func valueString(value any) (string, error) {
	bytes, err := valueBytes(value)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}
