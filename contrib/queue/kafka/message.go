package kafka

import (
	"errors"
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"github.com/twmb/franz-go/pkg/kgo"
)

const messageIDHeader = "x-queue-message-id"

// encodeRecord 深度复制通用消息为 Kafka Record，并用专用 Header 保留消息 ID。
func encodeRecord(topic string, message *queue.Message) *kgo.Record {
	headers := make([]kgo.RecordHeader, 0, len(message.Headers)+1)
	for _, header := range message.Headers {
		if header.Key == messageIDHeader {
			continue
		}
		headers = append(headers, kgo.RecordHeader{
			Key:   header.Key,
			Value: append([]byte(nil), header.Value...),
		})
	}
	headers = append(headers, kgo.RecordHeader{
		Key:   messageIDHeader,
		Value: []byte(message.ID),
	})
	return &kgo.Record{
		Topic:     topic,
		Key:       append([]byte(nil), message.Key...),
		Value:     append([]byte(nil), message.Body...),
		Headers:   headers,
		Timestamp: message.Timestamp,
	}
}

// decodeRecord 深度复制 Kafka Record 为 Delivery；nil Record 作为解码错误交给统一 Handler。
func decodeRecord(record *kgo.Record) queue.Delivery {
	if record == nil {
		return queue.Delivery{Err: errors.New("decode Kafka record: record is nil")}
	}
	messageID := ""
	headers := make([]queue.Header, 0, len(record.Headers))
	for _, header := range record.Headers {
		if header.Key == messageIDHeader {
			messageID = string(header.Value)
			continue
		}
		headers = append(headers, queue.Header{
			Key:   header.Key,
			Value: append([]byte(nil), header.Value...),
		})
	}
	if messageID == "" {
		messageID = fmt.Sprintf("%s:%d:%d", record.Topic, record.Partition, record.Offset)
	}
	return queue.Delivery{Message: &queue.Message{
		ID:        messageID,
		Key:       append([]byte(nil), record.Key...),
		Body:      append([]byte(nil), record.Value...),
		Headers:   headers,
		Timestamp: record.Timestamp,
	}}
}
