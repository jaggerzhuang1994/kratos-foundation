package kafka

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestNilManagerHasNoConnection(t *testing.T) {
	var manager *ClientFactory
	if manager.HasConnection("main") {
		t.Fatal("nil ClientFactory reported a connection")
	}
	if _, err := manager.NewProducerClient("main"); err == nil {
		t.Fatal("nil ClientFactory created a producer")
	}
}

func TestNewClientFactoryValidatesAndBuildsEmptySnapshot(t *testing.T) {
	shared, release, err := foundationlog.NewSharedState(foundationlog.Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std: foundationlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: foundationlog.FileConfig{OutputConfig: foundationlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	manager, err := NewClientFactory(foundationlog.NewLogger(shared), testconfig.Empty(t))
	if err != nil {
		t.Fatal(err)
	}
	if manager == nil || manager.HasConnection("missing") {
		t.Fatalf("empty manager = %v", manager)
	}
}

func TestConnectionLookupAndClientCreationRejectUnknownWithoutDialing(t *testing.T) {
	manager := &ClientFactory{connections: map[string]connectionConfig{}}
	if _, err := manager.connection(" missing "); err == nil {
		t.Fatal("unknown connection accepted")
	}
	if _, err := manager.NewConsumerClient("missing"); err == nil {
		t.Fatal("unknown consumer connection dialed")
	}
}

func TestProducerAndConsumerClientsBuildFromValidatedSnapshotWithoutDialing(t *testing.T) {
	manager := &ClientFactory{connections: map[string]connectionConfig{
		"main": {
			Brokers:  []string{"127.0.0.1:1"},
			ClientId: stringp("foundation-test"),
			Producer: &config_pb.KafkaProducer{
				RequiredAcks: enumP(config_pb.KafkaRequiredAcks_KAFKA_REQUIRED_ACKS_ALL),
				Compression:  enumP(config_pb.KafkaCompression_KAFKA_COMPRESSION_GZIP),
			},
			Consumer: &config_pb.KafkaConsumer{
				SessionTimeout:    durationpb.New(time.Second),
				HeartbeatInterval: durationpb.New(100 * time.Millisecond),
				FetchMaxWait:      durationpb.New(10 * time.Millisecond),
			},
		},
	}}
	if !manager.HasConnection(" main ") {
		t.Fatal("HasConnection did not normalize the configured name")
	}
	producer, err := manager.NewProducerClient(" main ", kgo.ClientID("override"))
	if err != nil {
		t.Fatal(err)
	}
	producer.Close()
	consumer, err := manager.NewConsumerClient("main", kgo.ConsumeTopics("orders"))
	if err != nil {
		t.Fatal(err)
	}
	consumer.Close()
}

func TestNewNormalizesSnapshotsAndPropagatesConfigurationFailures(t *testing.T) {
	logger, _ := newProviderTestLogger(t)
	debug := "debug"
	manager, err := NewClientFactory(logger, testconfig.New(t, "kafka", &config_pb.Kafka{
		Log: &config_pb.ModuleLog{Level: &debug},
		Connections: map[string]*config_pb.KafkaConnection{
			" main ": {Brokers: []string{" broker:9092 "}},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !manager.HasConnection("main") || manager.logger.Level() != kgo.LogLevelDebug {
		t.Fatalf("manager snapshot = %#v, logger level = %v", manager.connections, manager.logger.Level())
	}
	if got := manager.connections["main"].GetBrokers(); len(got) != 1 || got[0] != " broker:9092 " {
		t.Fatalf("stored snapshot brokers = %#v", got)
	}

	duplicate := &config_pb.Kafka{Connections: map[string]*config_pb.KafkaConnection{
		"main":   {Brokers: []string{"one:9092"}},
		" main ": {Brokers: []string{"two:9092"}},
	}}
	if got, err := NewClientFactory(logger, testconfig.New(t, "kafka", duplicate)); got != nil || err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("NewClientFactory(duplicate names) = (%v, %v)", got, err)
	}

	wantErr := errors.New("module log rejected")
	if got, err := NewClientFactory(
		failingKafkaModuleLogger{Logger: logger, err: wantErr},
		testconfig.Empty(t),
	); got != nil || !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "configure kafka logger") {
		t.Fatalf("NewClientFactory(logger failure) = (%v, %v)", got, err)
	}
}

func TestNewBuildsProducerAndConsumerOptionsWithoutDialing(t *testing.T) {
	logger, _ := newProviderTestLogger(t)
	debug := "debug"
	clientID := "configured-client"
	allowAutoCreate := true
	insecure := true
	serverName := " kafka.local "
	username := " user "
	password := "secret"
	plain := config_pb.KafkaSASLMechanism_KAFKA_SASL_MECHANISM_PLAIN
	acks := config_pb.KafkaRequiredAcks_KAFKA_REQUIRED_ACKS_ALL
	compression := config_pb.KafkaCompression_KAFKA_COMPRESSION_GZIP
	batchBytes := int32(4096)
	fetchMin := int32(1024)
	fetchMax := int32(8192)
	manager, err := NewClientFactory(logger, testconfig.New(t, "kafka", &config_pb.Kafka{
		Log: &config_pb.ModuleLog{Level: &debug},
		Connections: map[string]*config_pb.KafkaConnection{
			"main": {
				Brokers:                []string{" broker-a:9092 ", "broker-b:9092"},
				ClientId:               &clientID,
				DialTimeout:            durationpb.New(2 * time.Second),
				AllowAutoTopicCreation: &allowAutoCreate,
				Tls: &config_pb.KafkaTLS{
					ServerName:         &serverName,
					InsecureSkipVerify: &insecure,
				},
				Sasl: &config_pb.KafkaSASL{
					Mechanism: &plain,
					Username:  &username,
					Password:  &password,
				},
				Producer: &config_pb.KafkaProducer{
					RequiredAcks:  &acks,
					Compression:   &compression,
					Linger:        durationpb.New(25 * time.Millisecond),
					BatchMaxBytes: &batchBytes,
				},
				Consumer: &config_pb.KafkaConsumer{
					SessionTimeout:    durationpb.New(3 * time.Second),
					HeartbeatInterval: durationpb.New(time.Second),
					RebalanceTimeout:  durationpb.New(4 * time.Second),
					FetchMinBytes:     &fetchMin,
					FetchMaxBytes:     &fetchMax,
					FetchMaxWait:      durationpb.New(250 * time.Millisecond),
				},
			},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}

	producer, err := manager.NewProducerClient(" main ", kgo.ClientID("producer-override"))
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	if got := producer.OptValue(kgo.SeedBrokers); !reflect.DeepEqual(got, []string{"broker-a:9092", "broker-b:9092"}) {
		t.Errorf("producer brokers = %#v", got)
	}
	if got := producer.OptValue(kgo.ClientID); got != "producer-override" {
		t.Errorf("producer client ID = %#v", got)
	}
	if got := producer.OptValue(kgo.DialTimeout); got != 2*time.Second {
		t.Errorf("producer dial timeout = %#v", got)
	}
	if got := producer.OptValue(kgo.AllowAutoTopicCreation); got != true {
		t.Errorf("producer auto topic creation = %#v", got)
	}
	if got := producer.OptValue(kgo.RequiredAcks); !reflect.DeepEqual(got, kgo.AllISRAcks()) {
		t.Errorf("producer required acks = %#v", got)
	}
	if got := producer.OptValue(kgo.ProducerLinger); got != 25*time.Millisecond {
		t.Errorf("producer linger = %#v", got)
	}
	if got := producer.OptValue(kgo.ProducerBatchMaxBytes); got != int32(4096) {
		t.Errorf("producer batch max bytes = %#v", got)
	}
	if got := producer.OptValue(kgo.ProducerBatchCompression); !reflect.DeepEqual(got, []kgo.CompressionCodec{kgo.GzipCompression()}) {
		t.Errorf("producer compression = %#v", got)
	}
	if got := producer.OptValue(kgo.SASL); got == nil {
		t.Error("producer SASL option is missing")
	}

	consumer, err := manager.NewConsumerClient("main", kgo.ConsumeTopics("orders"))
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	for _, test := range []struct {
		name   string
		option any
		want   any
	}{
		{name: "client ID", option: kgo.ClientID, want: clientID},
		{name: "session timeout", option: kgo.SessionTimeout, want: 3 * time.Second},
		{name: "heartbeat interval", option: kgo.HeartbeatInterval, want: time.Second},
		{name: "rebalance timeout", option: kgo.RebalanceTimeout, want: 4 * time.Second},
		{name: "fetch min bytes", option: kgo.FetchMinBytes, want: int32(1024)},
		{name: "fetch max bytes", option: kgo.FetchMaxBytes, want: int32(8192)},
		{name: "fetch max wait", option: kgo.FetchMaxWait, want: 250 * time.Millisecond},
	} {
		if got := consumer.OptValue(test.option); !reflect.DeepEqual(got, test.want) {
			t.Errorf("consumer %s = %#v, want %#v", test.name, got, test.want)
		}
	}
	if got := consumer.GetConsumeTopics(); !reflect.DeepEqual(got, []string{"orders"}) {
		t.Errorf("consumer topics = %#v", got)
	}
}
