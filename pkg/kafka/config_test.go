package kafka

import (
	"strings"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestValidateConnectionRejectsUnsafeProducerConsumerAndSASLValues(t *testing.T) {
	valid := func() *config_pb.KafkaConnection { return &config_pb.KafkaConnection{Brokers: []string{"broker:9092"}} }
	for _, change := range []func(*config_pb.KafkaConnection){
		func(c *config_pb.KafkaConnection) { c.Brokers = []string{" "} },
		func(c *config_pb.KafkaConnection) { c.Producer = &config_pb.KafkaProducer{BatchMaxBytes: new(int32)} },
		func(c *config_pb.KafkaConnection) {
			c.Consumer = &config_pb.KafkaConsumer{FetchMinBytes: int32p(2), FetchMaxBytes: int32p(1)}
		},
		func(c *config_pb.KafkaConnection) { c.Sasl = &config_pb.KafkaSASL{Username: stringp("user")} },
	} {
		connection := valid()
		change(connection)
		if err := validateConnection("main", connection); err == nil {
			t.Fatalf("validateConnection accepted %#v", connection)
		}
	}
	if err := validateConnection("main", valid()); err != nil {
		t.Fatalf("valid connection = %v", err)
	}
}

func TestValidateConnectionBoundariesAndTLS(t *testing.T) {
	connection := &config_pb.KafkaConnection{Brokers: []string{"broker:9092"}, Consumer: &config_pb.KafkaConsumer{SessionTimeout: durationpb.New(100 * time.Millisecond), HeartbeatInterval: durationpb.New(time.Millisecond), FetchMaxWait: durationpb.New(10 * time.Millisecond)}}
	if err := validateConnection("main", connection); err != nil {
		t.Fatalf("boundary config = %v", err)
	}
	connection.Tls = &config_pb.KafkaTLS{CertFile: stringp("cert.pem")}
	if err := validateConnection("main", connection); err == nil {
		t.Fatal("unpaired TLS certificate accepted")
	}
}

func TestValidateConnectionRejectsEveryUnsafeBoundary(t *testing.T) {
	valid := func() *config_pb.KafkaConnection {
		return &config_pb.KafkaConnection{Brokers: []string{"broker:9092"}}
	}
	for _, test := range []struct {
		name       string
		connection func() *config_pb.KafkaConnection
	}{
		{name: "nil", connection: func() *config_pb.KafkaConnection { return nil }},
		{name: "no brokers", connection: func() *config_pb.KafkaConnection { return &config_pb.KafkaConnection{} }},
		{name: "dial timeout", connection: func() *config_pb.KafkaConnection { c := valid(); c.DialTimeout = durationpb.New(0); return c }},
		{name: "SASL username", connection: func() *config_pb.KafkaConnection {
			c := valid()
			plain := config_pb.KafkaSASLMechanism_KAFKA_SASL_MECHANISM_PLAIN
			c.Sasl = &config_pb.KafkaSASL{Mechanism: &plain}
			return c
		}},
		{name: "SASL unsupported", connection: func() *config_pb.KafkaConnection {
			c := valid()
			unsupported := config_pb.KafkaSASLMechanism(99)
			c.Sasl = &config_pb.KafkaSASL{Mechanism: &unsupported}
			return c
		}},
		{name: "producer negative linger", connection: func() *config_pb.KafkaConnection {
			c := valid()
			c.Producer = &config_pb.KafkaProducer{Linger: durationpb.New(-time.Millisecond)}
			return c
		}},
		{name: "producer excessive linger", connection: func() *config_pb.KafkaConnection {
			c := valid()
			c.Producer = &config_pb.KafkaProducer{Linger: durationpb.New(maxKafkaLinger + time.Millisecond)}
			return c
		}},
		{name: "producer acks", connection: func() *config_pb.KafkaConnection {
			c := valid()
			value := config_pb.KafkaRequiredAcks(99)
			c.Producer = &config_pb.KafkaProducer{RequiredAcks: &value}
			return c
		}},
		{name: "producer compression", connection: func() *config_pb.KafkaConnection {
			c := valid()
			value := config_pb.KafkaCompression(99)
			c.Producer = &config_pb.KafkaProducer{Compression: &value}
			return c
		}},
		{name: "consumer session", connection: func() *config_pb.KafkaConnection {
			c := valid()
			c.Consumer = &config_pb.KafkaConsumer{SessionTimeout: durationpb.New(minKafkaGroupTimeout - time.Millisecond)}
			return c
		}},
		{name: "consumer heartbeat", connection: func() *config_pb.KafkaConnection {
			c := valid()
			c.Consumer = &config_pb.KafkaConsumer{HeartbeatInterval: durationpb.New(0)}
			return c
		}},
		{name: "consumer rebalance", connection: func() *config_pb.KafkaConnection {
			c := valid()
			c.Consumer = &config_pb.KafkaConsumer{RebalanceTimeout: durationpb.New(minKafkaGroupTimeout - time.Millisecond)}
			return c
		}},
		{name: "consumer fetch wait", connection: func() *config_pb.KafkaConnection {
			c := valid()
			c.Consumer = &config_pb.KafkaConsumer{FetchMaxWait: durationpb.New(minKafkaFetchWait - time.Millisecond)}
			return c
		}},
		{name: "consumer fetch min", connection: func() *config_pb.KafkaConnection {
			c := valid()
			c.Consumer = &config_pb.KafkaConsumer{FetchMinBytes: int32p(0)}
			return c
		}},
		{name: "consumer fetch max", connection: func() *config_pb.KafkaConnection {
			c := valid()
			c.Consumer = &config_pb.KafkaConsumer{FetchMaxBytes: int32p(0)}
			return c
		}},
		{name: "consumer heartbeat versus session", connection: func() *config_pb.KafkaConnection {
			c := valid()
			c.Consumer = &config_pb.KafkaConsumer{SessionTimeout: durationpb.New(time.Second), HeartbeatInterval: durationpb.New(time.Second)}
			return c
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateConnection("main", test.connection()); err == nil || !strings.Contains(err.Error(), "main") {
				t.Fatalf("validateConnection() error = %v", err)
			}
		})
	}
	if err := validateConnection("", valid()); err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("empty name error = %v", err)
	}
}

func TestLoadConfigReturnsIndependentValidatedSnapshots(t *testing.T) {
	configured := &config_pb.Kafka{
		Connections: map[string]*config_pb.KafkaConnection{
			"main": {Brokers: []string{"broker:9092"}},
		},
	}
	configManager := testconfig.New(t, "kafka", configured)
	first, err := loadConfig(configManager)
	if err != nil {
		t.Fatal(err)
	}
	first.Connections["main"].Brokers[0] = "mutated:9092"
	second, err := loadConfig(configManager)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.GetConnections()["main"].GetBrokers(); len(got) != 1 || got[0] != "broker:9092" {
		t.Fatalf("second load brokers = %#v", got)
	}
}
