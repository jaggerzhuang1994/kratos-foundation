package kafka

import (
	"reflect"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestProducerOptionMappingsAreVisibleOnLocalClient(t *testing.T) {
	for _, test := range []struct {
		name                string
		acks                config_pb.KafkaRequiredAcks
		want                kgo.Acks
		idempotencyDisabled bool
	}{
		{name: "none", acks: config_pb.KafkaRequiredAcks_KAFKA_REQUIRED_ACKS_NONE, want: kgo.NoAck(), idempotencyDisabled: true},
		{name: "leader", acks: config_pb.KafkaRequiredAcks_KAFKA_REQUIRED_ACKS_LEADER, want: kgo.LeaderAck(), idempotencyDisabled: true},
		{name: "all ISR", acks: config_pb.KafkaRequiredAcks_KAFKA_REQUIRED_ACKS_ALL, want: kgo.AllISRAcks()},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, err := kgo.NewClient(append(
				[]kgo.Opt{kgo.SeedBrokers("127.0.0.1:1")},
				producerOptions(&config_pb.KafkaProducer{RequiredAcks: &test.acks})...,
			)...)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if got := client.OptValue(kgo.RequiredAcks); !reflect.DeepEqual(got, test.want) {
				t.Errorf("required acks = %#v, want %#v", got, test.want)
			}
			if got := client.OptValue(kgo.DisableIdempotentWrite); got != test.idempotencyDisabled {
				t.Errorf("DisableIdempotentWrite = %#v, want %t", got, test.idempotencyDisabled)
			}
		})
	}
}

func TestCompressionCodecMapsEveryConfiguredValue(t *testing.T) {
	for _, test := range []struct {
		value config_pb.KafkaCompression
		want  kgo.CompressionCodec
	}{
		{value: config_pb.KafkaCompression_KAFKA_COMPRESSION_NONE, want: kgo.NoCompression()},
		{value: config_pb.KafkaCompression_KAFKA_COMPRESSION_GZIP, want: kgo.GzipCompression()},
		{value: config_pb.KafkaCompression_KAFKA_COMPRESSION_SNAPPY, want: kgo.SnappyCompression()},
		{value: config_pb.KafkaCompression_KAFKA_COMPRESSION_LZ4, want: kgo.Lz4Compression()},
		{value: config_pb.KafkaCompression_KAFKA_COMPRESSION_ZSTD, want: kgo.ZstdCompression()},
		{value: config_pb.KafkaCompression_KAFKA_COMPRESSION_UNSPECIFIED, want: kgo.SnappyCompression()},
		{value: config_pb.KafkaCompression(99), want: kgo.NoCompression()},
	} {
		if got := compressionCodec(test.value); !reflect.DeepEqual(got, test.want) {
			t.Errorf("compressionCodec(%v) = %#v, want %#v", test.value, got, test.want)
		}
	}
}
