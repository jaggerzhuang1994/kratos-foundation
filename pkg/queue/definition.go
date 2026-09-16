package queue

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	errMessageDecode     = errors.New("queue message decode failed")
	errMessageValidation = errors.New("queue message validation failed")
)

// Codec 编解码一种业务消息；实现必须支持并发调用，返回的数据由调用方独立拥有。
type Codec[T any] interface {
	Encode(T) ([]byte, error)
	Decode([]byte) (T, error)
}

// JSONCodec 使用标准库 JSON 编解码消息。
type JSONCodec[T any] struct{}

// Encode 将消息编码为 JSON。
func (JSONCodec[T]) Encode(message T) ([]byte, error) { return json.Marshal(message) }

// Decode 将 JSON 解码为消息；业务字段约束由 Definition.Validate 校验。
func (JSONCodec[T]) Decode(data []byte) (T, error) {
	var message T
	err := json.Unmarshal(data, &message)
	return message, err
}

// Definition 定义一个队列的消息契约，发布与消费必须使用相同定义。
// MessageType 是显式稳定名称，Version 为正整数，持久化 Task.Type 为 MessageType.vVersion。
// Queue 是逻辑名称，Store 的物理队列仍由组装层绑定。Codec=nil 使用 JSON。
// 构造函数复制定义；Codec 和 Validate 被共享，必须并发安全且不能在运行期修改。
type Definition[T any] struct {
	Queue       string
	MessageType string
	Version     int
	Codec       Codec[T]
	Validate    func(T) error
}

func (d Definition[T]) resolve() (Definition[T], error) {
	if d.Queue == "" || strings.TrimSpace(d.Queue) != d.Queue || d.MessageType == "" || strings.TrimSpace(d.MessageType) != d.MessageType || d.Version < 1 {
		return d, errors.New("queue definition requires a queue, stable message type and positive version")
	}
	if d.Codec == nil {
		d.Codec = JSONCodec[T]{}
	}
	return d, nil
}

func (d Definition[T]) taskType() string { return fmt.Sprintf("%s.v%d", d.MessageType, d.Version) }
