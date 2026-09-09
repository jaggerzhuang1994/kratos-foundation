package redis

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type componentConfig = *config_pb.Redis

type connectionOption = *config_pb.RedisOption

// defaultConfig 返回不会被调用方共享修改的 Redis 默认配置。
func defaultConfig() componentConfig {
	return &config_pb.Redis{
		Default:     proto.String("default"),
		Connections: nil,
		Log:         nil,
		Tracing: &config_pb.RedisTracing{
			DbStatement:   proto.Bool(true),
			CallerEnabled: proto.Bool(true),
			DialFilter:    proto.Bool(true),
		},
		Metrics: nil,
	}
}

// loadConfig 合并默认值并在创建任何 client 前完成配置校验。
func loadConfig(manager config.Manager) (componentConfig, error) {
	effective := new(config_pb.Redis)
	if err := manager.Load("redis", effective, defaultConfig()); err != nil {
		return nil, err
	}
	if err := validateConfig(effective); err != nil {
		return nil, fmt.Errorf("validate redis config: %w", err)
	}
	return effective, nil
}

// validateConfig 校验默认连接引用和每个具名连接，确保延迟创建不会把配置错误推迟到请求期。
func validateConfig(config componentConfig) error {
	if config == nil {
		return fmt.Errorf("config is nil")
	}
	if err := config.GetLog().ValidateAll(); err != nil {
		return fmt.Errorf("module log: %w", err)
	}
	defaultName := strings.TrimSpace(config.GetDefault())
	if defaultName == "" {
		return fmt.Errorf("default connection is required")
	}
	if defaultName != config.GetDefault() {
		return fmt.Errorf("default connection %q contains surrounding whitespace", config.GetDefault())
	}
	if _, ok := config.GetConnections()[defaultName]; !ok {
		return fmt.Errorf("default connection %q is not configured", defaultName)
	}
	for name, option := range config.GetConnections() {
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" {
			return fmt.Errorf("connection name is required")
		}
		if trimmedName != name {
			return fmt.Errorf("connection name %q contains surrounding whitespace", name)
		}
		if option == nil {
			return fmt.Errorf("connection %q option is nil", name)
		}
		if err := validateConnectionOption(name, option); err != nil {
			return err
		}
	}
	return nil
}

// validateConnectionOption 校验 go-redis 不会主动拒绝、却会导致隐式回退或运行期失败的字段。
func validateConnectionOption(name string, option connectionOption) error {
	address := strings.TrimSpace(option.GetAddr())
	if address == "" {
		return fmt.Errorf("connection %q address is required", name)
	}
	if address != option.GetAddr() {
		return fmt.Errorf("connection %q address contains surrounding whitespace", name)
	}
	network := strings.TrimSpace(option.GetNetwork())
	if network != option.GetNetwork() {
		return fmt.Errorf("connection %q network contains surrounding whitespace", name)
	}
	if network != "" && network != "tcp" && network != "unix" {
		return fmt.Errorf("connection %q network %q must be tcp or unix", name, option.GetNetwork())
	}
	protocol := option.GetProtocol()
	if protocol != 0 && protocol != 2 && protocol != 3 {
		return fmt.Errorf("connection %q protocol %d must be 2 or 3", name, protocol)
	}

	// 同类整数集中校验，防止 go-redis 把负值悄悄改成与配置意图不同的默认值。
	integerFields := []struct {
		field   string
		value   int32
		minimum int32
	}{
		{"db", option.GetDb(), 0},
		{"max_retries", option.GetMaxRetries(), -1},
		{"dialer_retries", option.GetDialerRetries(), 0},
		{"read_buffer_size", option.GetReadBufferSize(), 0},
		{"write_buffer_size", option.GetWriteBufferSize(), 0},
		{"pool_size", option.GetPoolSize(), 0},
		{"max_concurrent_dials", option.GetMaxConcurrentDials(), 0},
		{"min_idle_conns", option.GetMinIdleConns(), 0},
		{"max_idle_conns", option.GetMaxIdleConns(), 0},
		{"max_active_conns", option.GetMaxActiveConns(), 0},
		{"failing_timeout_seconds", option.GetFailingTimeoutSeconds(), 0},
	}
	for _, field := range integerFields {
		if field.value < field.minimum {
			return fmt.Errorf(
				"connection %q %s must be at least %d",
				name,
				field.field,
				field.minimum,
			)
		}
	}
	if maximum := option.GetMaxIdleConns(); maximum > 0 && option.GetMinIdleConns() > maximum {
		return fmt.Errorf("connection %q min_idle_conns exceeds max_idle_conns", name)
	}

	durationFields := []struct {
		field           string
		value           *durationpb.Duration
		allowedNegative []time.Duration
	}{
		{"min_retry_backoff", option.GetMinRetryBackoff(), []time.Duration{-1}},
		{"max_retry_backoff", option.GetMaxRetryBackoff(), []time.Duration{-1}},
		{"dial_timeout", option.GetDialTimeout(), nil},
		{"dialer_retry_timeout", option.GetDialerRetryTimeout(), nil},
		{"read_timeout", option.GetReadTimeout(), []time.Duration{-1, -2}},
		{"write_timeout", option.GetWriteTimeout(), []time.Duration{-1, -2}},
		{"pool_timeout", option.GetPoolTimeout(), nil},
		{"conn_max_idle_time", option.GetConnMaxIdleTime(), []time.Duration{-1}},
		{"conn_max_lifetime", option.GetConnMaxLifetime(), nil},
	}
	for _, field := range durationFields {
		if err := validateDuration(field.field, field.value, field.allowedNegative...); err != nil {
			return fmt.Errorf("connection %q: %w", name, err)
		}
	}
	return nil
}

// validateDuration 接受非负时长及 go-redis 明确定义的少数负数哨兵值。
func validateDuration(
	field string,
	value *durationpb.Duration,
	allowedNegative ...time.Duration,
) error {
	if value == nil {
		return nil
	}
	if err := value.CheckValid(); err != nil {
		return fmt.Errorf("%s is invalid: %w", field, err)
	}
	duration := value.AsDuration()
	if duration >= 0 {
		return nil
	}
	if slices.Contains(allowedNegative, duration) {
		return nil
	}
	return fmt.Errorf("%s has unsupported negative value %s", field, duration)
}
