package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
	"os"
	"strings"
)

// newTLSConfig 构造最低 TLS 1.2 的连接配置，并按需追加 CA 与客户端证书。
func newTLSConfig(config *config_pb.KafkaTLS) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: strings.TrimSpace(config.GetServerName()),
		// 仅当应用显式配置时透传该兼容开关；默认仍执行证书链和主机名校验。
		InsecureSkipVerify: config.GetInsecureSkipVerify(), //nolint:gosec
	}
	if caFile := strings.TrimSpace(config.GetCaFile()); caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read Kafka TLS CA file %q: %w", caFile, err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("kafka TLS CA file %q contains no certificates", caFile)
		}
		tlsConfig.RootCAs = roots
	}
	if certFile := strings.TrimSpace(config.GetCertFile()); certFile != "" {
		certificate, err := tls.LoadX509KeyPair(certFile, strings.TrimSpace(config.GetKeyFile()))
		if err != nil {
			return nil, fmt.Errorf("load Kafka TLS client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}
	return tlsConfig, nil
}

// newSASLMechanism 把公开枚举转换为 franz-go 的 PLAIN 或 SCRAM 认证实现。
func newSASLMechanism(config *config_pb.KafkaSASL) (sasl.Mechanism, error) {
	username := strings.TrimSpace(config.GetUsername())
	password := config.GetPassword()
	switch config.GetMechanism() {
	case config_pb.KafkaSASLMechanism_KAFKA_SASL_MECHANISM_UNSPECIFIED:
		return nil, nil
	case config_pb.KafkaSASLMechanism_KAFKA_SASL_MECHANISM_PLAIN:
		return plain.Plain(func(context.Context) (plain.Auth, error) {
			return plain.Auth{User: username, Pass: password}, nil
		}), nil
	case config_pb.KafkaSASLMechanism_KAFKA_SASL_MECHANISM_SCRAM_SHA_256:
		return scram.Sha256(func(context.Context) (scram.Auth, error) {
			return scram.Auth{User: username, Pass: password}, nil
		}), nil
	case config_pb.KafkaSASLMechanism_KAFKA_SASL_MECHANISM_SCRAM_SHA_512:
		return scram.Sha512(func(context.Context) (scram.Auth, error) {
			return scram.Auth{User: username, Pass: password}, nil
		}), nil
	default:
		return nil, fmt.Errorf(
			"unsupported Kafka SASL mechanism %q",
			config.GetMechanism().String(),
		)
	}
}
