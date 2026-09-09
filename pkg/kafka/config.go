package kafka

import (
	"fmt"
	"strings"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

type componentConfig = *config_pb.Kafka

type connectionConfig = *config_pb.KafkaConnection

// defaultConfig 只作为 Manager.Load 的合并模板，Manager 会先复制再写入。
var defaultConfig = &config_pb.Kafka{}

// loadConfig 读取并校验 Kafka 顶层配置，返回调用方可独占持有的快照。
func loadConfig(manager config.Manager) (componentConfig, error) {
	effective := new(config_pb.Kafka)
	if err := manager.Load("kafka", effective, defaultConfig); err != nil {
		return nil, err
	}
	if err := effective.ValidateAll(); err != nil {
		return nil, fmt.Errorf("validate kafka config: %w", err)
	}
	return effective, nil
}

const (
	minKafkaFetchWait    = 10 * time.Millisecond
	minKafkaGroupTimeout = 100 * time.Millisecond
	maxKafkaLinger       = time.Minute
)

// validateConnection 在创建任何客户端前校验连接、安全及生产消费约束。
func validateConnection(name string, config connectionConfig) error {
	if name == "" {
		return fmt.Errorf("kafka connection name is required")
	}
	if config == nil {
		return fmt.Errorf("kafka connection %q is nil", name)
	}
	if len(config.GetBrokers()) == 0 {
		return fmt.Errorf("kafka connection %q requires at least one broker", name)
	}
	for _, broker := range config.GetBrokers() {
		if strings.TrimSpace(broker) == "" {
			return fmt.Errorf("kafka connection %q contains an empty broker", name)
		}
	}
	if config.DialTimeout != nil && config.GetDialTimeout().AsDuration() <= 0 {
		return fmt.Errorf("kafka connection %q dial_timeout must be positive", name)
	}
	if tlsConfig := config.GetTls(); tlsConfig != nil {
		hasCertificate := strings.TrimSpace(tlsConfig.GetCertFile()) != ""
		hasKey := strings.TrimSpace(tlsConfig.GetKeyFile()) != ""
		if hasCertificate != hasKey {
			return fmt.Errorf(
				"kafka connection %q TLS cert_file and key_file must be configured together",
				name,
			)
		}
	}
	if sasl := config.GetSasl(); sasl != nil {
		switch sasl.GetMechanism() {
		case config_pb.KafkaSASLMechanism_KAFKA_SASL_MECHANISM_UNSPECIFIED:
			if sasl.GetUsername() == "" && sasl.GetPassword() == "" {
				break
			}
			return fmt.Errorf(
				"kafka connection %q SASL mechanism is required when credentials are set",
				name,
			)
		case config_pb.KafkaSASLMechanism_KAFKA_SASL_MECHANISM_PLAIN,
			config_pb.KafkaSASLMechanism_KAFKA_SASL_MECHANISM_SCRAM_SHA_256,
			config_pb.KafkaSASLMechanism_KAFKA_SASL_MECHANISM_SCRAM_SHA_512:
			if strings.TrimSpace(sasl.GetUsername()) == "" {
				return fmt.Errorf("kafka connection %q SASL username is required", name)
			}
		default:
			return fmt.Errorf(
				"kafka connection %q has unsupported SASL mechanism %q",
				name,
				sasl.GetMechanism().String(),
			)
		}
	}
	if err := validateProducer(name, config.GetProducer()); err != nil {
		return err
	}
	if err := validateConsumer(name, config.GetConsumer()); err != nil {
		return err
	}
	return nil
}

// validateProducer 拒绝 franz-go 无法安全解释的生产者取值。
func validateProducer(name string, config *config_pb.KafkaProducer) error {
	if config == nil {
		return nil
	}
	if config.Linger != nil && config.GetLinger().AsDuration() < 0 {
		return fmt.Errorf("kafka connection %q producer linger cannot be negative", name)
	}
	if config.Linger != nil && config.GetLinger().AsDuration() > maxKafkaLinger {
		return fmt.Errorf("kafka connection %q producer linger cannot exceed %s", name, maxKafkaLinger)
	}
	if config.BatchMaxBytes != nil && config.GetBatchMaxBytes() <= 0 {
		return fmt.Errorf(
			"kafka connection %q producer batch_max_bytes must be positive",
			name,
		)
	}
	if config.RequiredAcks != nil {
		switch config.GetRequiredAcks() {
		case config_pb.KafkaRequiredAcks_KAFKA_REQUIRED_ACKS_UNSPECIFIED,
			config_pb.KafkaRequiredAcks_KAFKA_REQUIRED_ACKS_NONE,
			config_pb.KafkaRequiredAcks_KAFKA_REQUIRED_ACKS_LEADER,
			config_pb.KafkaRequiredAcks_KAFKA_REQUIRED_ACKS_ALL:
		default:
			return fmt.Errorf(
				"kafka connection %q has unsupported required_acks %q",
				name,
				config.GetRequiredAcks().String(),
			)
		}
	}
	if config.Compression != nil {
		switch config.GetCompression() {
		case config_pb.KafkaCompression_KAFKA_COMPRESSION_UNSPECIFIED,
			config_pb.KafkaCompression_KAFKA_COMPRESSION_NONE,
			config_pb.KafkaCompression_KAFKA_COMPRESSION_GZIP,
			config_pb.KafkaCompression_KAFKA_COMPRESSION_SNAPPY,
			config_pb.KafkaCompression_KAFKA_COMPRESSION_LZ4,
			config_pb.KafkaCompression_KAFKA_COMPRESSION_ZSTD:
		default:
			return fmt.Errorf(
				"kafka connection %q has unsupported compression %q",
				name,
				config.GetCompression().String(),
			)
		}
	}
	return nil
}

// validateConsumer 校验消费时间和抓取大小之间的相对约束。
func validateConsumer(name string, config *config_pb.KafkaConsumer) error {
	if config == nil {
		return nil
	}
	if config.SessionTimeout != nil && config.GetSessionTimeout().AsDuration() < minKafkaGroupTimeout {
		return fmt.Errorf("kafka connection %q session_timeout must be at least %s", name, minKafkaGroupTimeout)
	}
	if config.HeartbeatInterval != nil &&
		config.GetHeartbeatInterval().AsDuration() <= 0 {
		return fmt.Errorf("kafka connection %q heartbeat_interval must be positive", name)
	}
	if config.RebalanceTimeout != nil && config.GetRebalanceTimeout().AsDuration() < minKafkaGroupTimeout {
		return fmt.Errorf("kafka connection %q rebalance_timeout must be at least %s", name, minKafkaGroupTimeout)
	}
	if config.FetchMaxWait != nil && config.GetFetchMaxWait().AsDuration() < minKafkaFetchWait {
		return fmt.Errorf("kafka connection %q fetch_max_wait must be at least %s", name, minKafkaFetchWait)
	}
	if config.FetchMinBytes != nil && config.GetFetchMinBytes() <= 0 {
		return fmt.Errorf("kafka connection %q fetch_min_bytes must be positive", name)
	}
	if config.FetchMaxBytes != nil && config.GetFetchMaxBytes() <= 0 {
		return fmt.Errorf("kafka connection %q fetch_max_bytes must be positive", name)
	}
	if config.FetchMinBytes != nil &&
		config.FetchMaxBytes != nil &&
		config.GetFetchMaxBytes() < config.GetFetchMinBytes() {
		return fmt.Errorf(
			"kafka connection %q fetch_max_bytes cannot be less than fetch_min_bytes",
			name,
		)
	}
	if config.SessionTimeout != nil &&
		config.HeartbeatInterval != nil &&
		config.GetHeartbeatInterval().AsDuration() >=
			config.GetSessionTimeout().AsDuration() {
		return fmt.Errorf(
			"kafka connection %q heartbeat_interval must be less than session_timeout",
			name,
		)
	}
	return nil
}
