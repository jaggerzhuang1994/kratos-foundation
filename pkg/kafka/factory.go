package kafka

import (
	"errors"
	"fmt"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
	"slices"
	"strings"
)

// ClientFactory 根据具名连接配置创建 Kafka client；返回的 client 由调用方持有和关闭。
type ClientFactory struct {
	// connections 构造期复制的具名连接配置，只读使用，不随配置热更新。
	connections map[string]connectionConfig
	// logger 供 Kafka 客户端使用的日志适配器。
	logger kgo.Logger
}

// NewClientFactory 在启动阶段校验并索引全部连接，使运行期建连不再重复解析配置。
func NewClientFactory(
	logger log.Logger,
	configManager foundationconfig.Manager,
) (*ClientFactory, error) {
	config, err := loadConfig(configManager)
	if err != nil {
		return nil, err
	}
	moduleLogger := logger.WithModule("kafka")
	connections := make(map[string]connectionConfig, len(config.GetConnections()))
	names := make([]string, 0, len(config.GetConnections()))
	for name := range config.GetConnections() {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, originalName := range names {
		connection := config.GetConnections()[originalName]
		name := originalName
		name = strings.TrimSpace(name)
		if err := validateConnection(name, connection); err != nil {
			return nil, err
		}
		if _, exists := connections[name]; exists {
			return nil, fmt.Errorf("kafka connection %q is configured more than once", name)
		}
		connections[name] = proto.CloneOf(connection)
	}
	return &ClientFactory{
		connections: connections,
		logger:      newKafkaLogger(moduleLogger),
	}, nil
}

// HasConnection 用于让扩展组件在创建运行时对象前校验连接引用，同时不暴露组件配置。
func (m *ClientFactory) HasConnection(name string) bool {
	if m == nil {
		return false
	}
	name = strings.TrimSpace(name)
	_, ok := m.connections[name]
	return ok
}

// NewProducerClient 创建 producer client，调用方必须负责关闭。
func (m *ClientFactory) NewProducerClient(
	connection string,
	options ...kgo.Opt,
) (*kgo.Client, error) {
	config, err := m.connection(connection)
	if err != nil {
		return nil, err
	}
	base, err := m.options(config)
	if err != nil {
		return nil, fmt.Errorf("configure kafka connection %q: %w", connection, err)
	}
	base = append(base, producerOptions(config.GetProducer())...)
	base = append(base, options...)
	client, err := kgo.NewClient(base...)
	if err != nil {
		return nil, fmt.Errorf("create kafka producer client %q: %w", connection, err)
	}
	return client, nil
}

// NewConsumerClient 创建 consumer client，调用方必须负责关闭，并用可取消 Context
// 控制 polling 生命周期。
func (m *ClientFactory) NewConsumerClient(
	connection string,
	options ...kgo.Opt,
) (*kgo.Client, error) {
	config, err := m.connection(connection)
	if err != nil {
		return nil, err
	}
	base, err := m.options(config)
	if err != nil {
		return nil, fmt.Errorf("configure kafka connection %q: %w", connection, err)
	}
	base = append(base, consumerOptions(config.GetConsumer())...)
	base = append(base, options...)
	client, err := kgo.NewClient(base...)
	if err != nil {
		return nil, fmt.Errorf("create kafka consumer client %q: %w", connection, err)
	}
	return client, nil
}

// connection 解析具名配置；nil ClientFactory 与未知名称都返回明确错误。
func (m *ClientFactory) connection(name string) (connectionConfig, error) {
	if m == nil {
		return nil, errors.New("kafka client factory is nil")
	}
	name = strings.TrimSpace(name)
	connection, ok := m.connections[name]
	if !ok {
		return nil, fmt.Errorf("kafka connection %q is not configured", name)
	}
	return connection, nil
}

// options 把连接级网络、安全和日志配置转换为 franz-go 基础选项。
func (m *ClientFactory) options(config connectionConfig) ([]kgo.Opt, error) {
	brokers := make([]string, len(config.GetBrokers()))
	for index, broker := range config.GetBrokers() {
		brokers[index] = strings.TrimSpace(broker)
	}
	options := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.WithLogger(m.logger),
	}
	if clientID := strings.TrimSpace(config.GetClientId()); clientID != "" {
		options = append(options, kgo.ClientID(clientID))
	}
	if config.DialTimeout != nil {
		options = append(options, kgo.DialTimeout(config.GetDialTimeout().AsDuration()))
	}
	if config.GetAllowAutoTopicCreation() {
		options = append(options, kgo.AllowAutoTopicCreation())
	}
	if config.GetTls() != nil {
		tlsConfig, err := newTLSConfig(config.GetTls())
		if err != nil {
			return nil, err
		}
		options = append(options, kgo.DialTLSConfig(tlsConfig))
	}
	if config.GetSasl() != nil {
		mechanism, err := newSASLMechanism(config.GetSasl())
		if err != nil {
			return nil, err
		}
		if mechanism != nil {
			options = append(options, kgo.SASL(mechanism))
		}
	}
	return options, nil
}
