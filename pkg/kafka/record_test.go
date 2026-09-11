package kafka

import (
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

func TestDecodeRecordPreservesExplicitIDAndDetachesMutableBytes(t *testing.T) {
	key := []byte("key")
	value := []byte("value")
	header := []byte("trace")
	record := &kgo.Record{
		Topic:     "orders",
		Partition: 1,
		Offset:    2,
		Key:       key,
		Value:     value,
		Headers: []kgo.RecordHeader{
			{Key: messageIDHeader, Value: []byte("message-1")},
			{Key: "trace", Value: header},
		},
		Timestamp: time.Unix(10, 0),
	}
	delivery := decodeRecord(record)
	if delivery.Err != nil || delivery.Message == nil || delivery.Message.ID != "message-1" || len(delivery.Message.Headers) != 1 {
		t.Fatalf("decoded delivery = %#v", delivery)
	}
	key[0], value[0], header[0] = 'X', 'X', 'X'
	if string(delivery.Message.Key) != "key" || string(delivery.Message.Body) != "value" || string(delivery.Message.Headers[0].Value) != "trace" {
		t.Fatalf("decoded message aliases record: %#v", delivery.Message)
	}
	if got := decodeRecord(nil); got.Err == nil || got.Message != nil {
		t.Fatalf("nil record delivery = %#v", got)
	}
}
