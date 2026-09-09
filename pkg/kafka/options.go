package kafka

import (
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/twmb/franz-go/pkg/kgo"
)

// producerOptions 把已校验的确认、压缩和批处理策略转换为生产者选项。
func producerOptions(config *config_pb.KafkaProducer) []kgo.Opt {
	if config == nil {
		return nil
	}
	var options []kgo.Opt
	if config.RequiredAcks != nil {
		switch config.GetRequiredAcks() {
		case config_pb.KafkaRequiredAcks_KAFKA_REQUIRED_ACKS_NONE:
			options = append(
				options,
				kgo.RequiredAcks(kgo.NoAck()),
				kgo.DisableIdempotentWrite(),
			)
		case config_pb.KafkaRequiredAcks_KAFKA_REQUIRED_ACKS_LEADER:
			options = append(
				options,
				kgo.RequiredAcks(kgo.LeaderAck()),
				kgo.DisableIdempotentWrite(),
			)
		case config_pb.KafkaRequiredAcks_KAFKA_REQUIRED_ACKS_ALL:
			options = append(options, kgo.RequiredAcks(kgo.AllISRAcks()))
		}
	}
	if config.Compression != nil {
		if config.GetCompression() !=
			config_pb.KafkaCompression_KAFKA_COMPRESSION_UNSPECIFIED {
			options = append(options, kgo.ProducerBatchCompression(
				compressionCodec(config.GetCompression()),
			))
		}
	}
	if config.Linger != nil {
		options = append(options, kgo.ProducerLinger(config.GetLinger().AsDuration()))
	}
	if config.BatchMaxBytes != nil {
		options = append(options, kgo.ProducerBatchMaxBytes(config.GetBatchMaxBytes()))
	}
	return options
}

// compressionCodec 把配置枚举映射为 franz-go 压缩编解码器。
func compressionCodec(value config_pb.KafkaCompression) kgo.CompressionCodec {
	switch value {
	case config_pb.KafkaCompression_KAFKA_COMPRESSION_NONE:
		return kgo.NoCompression()
	case config_pb.KafkaCompression_KAFKA_COMPRESSION_GZIP:
		return kgo.GzipCompression()
	case config_pb.KafkaCompression_KAFKA_COMPRESSION_LZ4:
		return kgo.Lz4Compression()
	case config_pb.KafkaCompression_KAFKA_COMPRESSION_ZSTD:
		return kgo.ZstdCompression()
	case config_pb.KafkaCompression_KAFKA_COMPRESSION_SNAPPY,
		config_pb.KafkaCompression_KAFKA_COMPRESSION_UNSPECIFIED:
		return kgo.SnappyCompression()
	default:
		return kgo.NoCompression()
	}
}

// consumerOptions 把已校验的会话、再均衡和抓取策略转换为消费者选项。
func consumerOptions(config *config_pb.KafkaConsumer) []kgo.Opt {
	if config == nil {
		return nil
	}
	var options []kgo.Opt
	if config.SessionTimeout != nil {
		options = append(options, kgo.SessionTimeout(config.GetSessionTimeout().AsDuration()))
	}
	if config.HeartbeatInterval != nil {
		options = append(
			options,
			kgo.HeartbeatInterval(config.GetHeartbeatInterval().AsDuration()),
		)
	}
	if config.RebalanceTimeout != nil {
		options = append(
			options,
			kgo.RebalanceTimeout(config.GetRebalanceTimeout().AsDuration()),
		)
	}
	if config.FetchMinBytes != nil {
		options = append(options, kgo.FetchMinBytes(config.GetFetchMinBytes()))
	}
	if config.FetchMaxBytes != nil {
		options = append(options, kgo.FetchMaxBytes(config.GetFetchMaxBytes()))
	}
	if config.FetchMaxWait != nil {
		options = append(options, kgo.FetchMaxWait(config.GetFetchMaxWait().AsDuration()))
	}
	return options
}
