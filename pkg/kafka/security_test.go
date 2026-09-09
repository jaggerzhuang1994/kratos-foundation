package kafka

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

func TestTLSAndSASLConstructionStayLocal(t *testing.T) {
	tlsConfig, err := newTLSConfig(&config_pb.KafkaTLS{ServerName: stringp(" broker.local "), InsecureSkipVerify: boolp(true)})
	if err != nil || tlsConfig.MinVersion != tls.VersionTLS12 || tlsConfig.ServerName != "broker.local" || !tlsConfig.InsecureSkipVerify {
		t.Fatalf("TLS config = %#v, %v", tlsConfig, err)
	}
	missing := filepath.Join(t.TempDir(), "missing.pem")
	if _, err := newTLSConfig(&config_pb.KafkaTLS{CaFile: stringp(missing)}); err == nil {
		t.Fatal("missing CA file accepted")
	}
	pem := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(pem, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newTLSConfig(&config_pb.KafkaTLS{CaFile: stringp(pem)}); err == nil {
		t.Fatal("invalid CA accepted")
	}
	for _, mechanism := range []config_pb.KafkaSASLMechanism{config_pb.KafkaSASLMechanism_KAFKA_SASL_MECHANISM_UNSPECIFIED, config_pb.KafkaSASLMechanism_KAFKA_SASL_MECHANISM_PLAIN, config_pb.KafkaSASLMechanism_KAFKA_SASL_MECHANISM_SCRAM_SHA_256, config_pb.KafkaSASLMechanism_KAFKA_SASL_MECHANISM_SCRAM_SHA_512} {
		got, err := newSASLMechanism(&config_pb.KafkaSASL{Mechanism: &mechanism, Username: stringp("user"), Password: stringp("secret")})
		if err != nil || (mechanism == config_pb.KafkaSASLMechanism_KAFKA_SASL_MECHANISM_UNSPECIFIED && got != nil) || (mechanism != config_pb.KafkaSASLMechanism_KAFKA_SASL_MECHANISM_UNSPECIFIED && got == nil) {
			t.Fatalf("SASL %v = %v, %v", mechanism, got, err)
		}
	}
}
