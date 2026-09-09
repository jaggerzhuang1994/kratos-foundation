package redis

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type loadErrorConfigManager struct {
	err error
}

func (m loadErrorConfigManager) Load(string, any, ...any) error {
	return m.err
}

func (loadErrorConfigManager) Subscribe(
	string,
	any,
	foundationconfig.Observer,
	...any,
) (func(), error) {
	return nil, errors.New("Subscribe must not be called by loadConfig")
}

func TestDefaultConfigReturnsIndependentDocumentedDefaults(t *testing.T) {
	first := defaultConfig()
	if first.GetDefault() != "default" {
		t.Fatalf("default connection = %q, want default", first.GetDefault())
	}
	if !first.GetTracing().GetDbStatement() ||
		!first.GetTracing().GetCallerEnabled() ||
		!first.GetTracing().GetDialFilter() {
		t.Fatalf("tracing defaults = %#v", first.GetTracing())
	}
	if first.GetLog() != nil || first.GetMetrics() != nil || first.GetConnections() != nil {
		t.Fatalf("unexpected optional defaults = %#v", first)
	}

	first.Default = proto.String("changed")
	first.Tracing.DbStatement = proto.Bool(false)
	first.Connections = map[string]*config_pb.RedisOption{
		"mutated": {Addr: proto.String("127.0.0.1:1")},
	}
	second := defaultConfig()
	if second.GetDefault() != "default" || !second.GetTracing().GetDbStatement() || second.GetConnections() != nil {
		t.Fatalf("defaultConfig returned shared mutable state: %#v", second)
	}
}

func TestLoadConfigMergesDefaultsAndValidatesSnapshot(t *testing.T) {
	configManager := testconfig.New(t, "redis", &config_pb.Redis{
		Connections: map[string]*config_pb.RedisOption{
			"default": {Addr: proto.String("127.0.0.1:6379")},
		},
	})

	loaded, err := loadConfig(configManager)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GetDefault() != "default" ||
		loaded.GetConnections()["default"].GetAddr() != "127.0.0.1:6379" {
		t.Fatalf("loaded Redis config = %#v", loaded)
	}
	if !loaded.GetTracing().GetDbStatement() ||
		!loaded.GetTracing().GetCallerEnabled() ||
		!loaded.GetTracing().GetDialFilter() {
		t.Fatalf("merged tracing defaults = %#v", loaded.GetTracing())
	}
}

func TestLoadConfigPreservesLoadErrorAndWrapsValidationError(t *testing.T) {
	cause := errors.New("source unavailable")
	if _, err := loadConfig(loadErrorConfigManager{err: cause}); !errors.Is(err, cause) {
		t.Fatalf("loadConfig error = %v, want source cause", err)
	}

	_, err := loadConfig(testconfig.Empty(t))
	if err == nil || !strings.Contains(err.Error(), "validate redis config") ||
		!strings.Contains(err.Error(), `default connection "default" is not configured`) {
		t.Fatalf("loadConfig invalid snapshot error = %v", err)
	}
}

func TestValidateConfigRejectsInvalidTopLevelStructure(t *testing.T) {
	tests := []struct {
		name    string
		config  componentConfig
		wantErr string
	}{
		{name: "nil config", wantErr: "config is nil"},
		{
			name: "invalid module log",
			config: &config_pb.Redis{
				Log: &config_pb.ModuleLog{Level: proto.String("verbose")},
			},
			wantErr: "module log",
		},
		{
			name: "empty default",
			config: &config_pb.Redis{
				Default: proto.String(""),
			},
			wantErr: "default connection is required",
		},
		{
			name: "default whitespace",
			config: &config_pb.Redis{
				Default: proto.String(" default "),
				Connections: map[string]*config_pb.RedisOption{
					" default ": {Addr: proto.String("127.0.0.1:6379")},
				},
			},
			wantErr: "default connection \" default \" contains surrounding whitespace",
		},
		{
			name: "unknown default",
			config: &config_pb.Redis{
				Default: proto.String("default"),
				Connections: map[string]*config_pb.RedisOption{
					"other": {Addr: proto.String("127.0.0.1:6379")},
				},
			},
			wantErr: `default connection "default" is not configured`,
		},
		{
			name: "empty connection name",
			config: &config_pb.Redis{
				Default: proto.String("default"),
				Connections: map[string]*config_pb.RedisOption{
					"default": {Addr: proto.String("127.0.0.1:6379")},
					"":        {Addr: proto.String("127.0.0.1:6380")},
				},
			},
			wantErr: "connection name is required",
		},
		{
			name: "connection name whitespace",
			config: &config_pb.Redis{
				Default: proto.String("default"),
				Connections: map[string]*config_pb.RedisOption{
					"default": {Addr: proto.String("127.0.0.1:6379")},
					" cache ": {Addr: proto.String("127.0.0.1:6380")},
				},
			},
			wantErr: `connection name " cache " contains surrounding whitespace`,
		},
		{
			name: "nil connection option",
			config: &config_pb.Redis{
				Default: proto.String("default"),
				Connections: map[string]*config_pb.RedisOption{
					"default": {Addr: proto.String("127.0.0.1:6379")},
					"cache":   nil,
				},
			},
			wantErr: `connection "cache" option is nil`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.config)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateConfig error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}

	if err := validateConfig(validRedisConfig()); err != nil {
		t.Fatalf("valid Redis config rejected: %v", err)
	}

	invalidConnection := validRedisConfig()
	invalidConnection.Connections["default"].Addr = proto.String("")
	if err := validateConfig(invalidConnection); err == nil ||
		!strings.Contains(err.Error(), `connection "default" address is required`) {
		t.Fatalf("connection validation error was not propagated: %v", err)
	}
}

func TestValidateConnectionOptionRejectsAddressNetworkAndProtocolErrors(t *testing.T) {
	tests := []struct {
		name    string
		option  connectionOption
		wantErr string
	}{
		{
			name:    "empty address",
			option:  &config_pb.RedisOption{},
			wantErr: "address is required",
		},
		{
			name:    "address whitespace",
			option:  &config_pb.RedisOption{Addr: proto.String(" 127.0.0.1:6379")},
			wantErr: "address contains surrounding whitespace",
		},
		{
			name: "network whitespace",
			option: &config_pb.RedisOption{
				Addr:    proto.String("127.0.0.1:6379"),
				Network: proto.String("tcp "),
			},
			wantErr: "network contains surrounding whitespace",
		},
		{
			name: "unsupported network",
			option: &config_pb.RedisOption{
				Addr:    proto.String("127.0.0.1:6379"),
				Network: proto.String("udp"),
			},
			wantErr: "must be tcp or unix",
		},
		{
			name: "unsupported protocol",
			option: &config_pb.RedisOption{
				Addr:     proto.String("127.0.0.1:6379"),
				Protocol: proto.Int32(1),
			},
			wantErr: "protocol 1 must be 2 or 3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConnectionOption("cache", tt.option)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateConnectionOption error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}

	for _, network := range []string{"", "tcp", "unix"} {
		for _, protocol := range []int32{0, 2, 3} {
			option := &config_pb.RedisOption{
				Addr:       proto.String("127.0.0.1:6379"),
				Network:    proto.String(network),
				Protocol:   proto.Int32(protocol),
				MaxRetries: proto.Int32(-1),
			}
			if err := validateConnectionOption("cache", option); err != nil {
				t.Fatalf("valid network %q protocol %d rejected: %v", network, protocol, err)
			}
		}
	}
}

func TestValidateConnectionOptionRejectsUnsupportedIntegerValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*config_pb.RedisOption)
	}{
		{name: "db", mutate: func(o *config_pb.RedisOption) { o.Db = proto.Int32(-1) }},
		{name: "max_retries", mutate: func(o *config_pb.RedisOption) { o.MaxRetries = proto.Int32(-2) }},
		{name: "dialer_retries", mutate: func(o *config_pb.RedisOption) { o.DialerRetries = proto.Int32(-1) }},
		{name: "read_buffer_size", mutate: func(o *config_pb.RedisOption) { o.ReadBufferSize = proto.Int32(-1) }},
		{name: "write_buffer_size", mutate: func(o *config_pb.RedisOption) { o.WriteBufferSize = proto.Int32(-1) }},
		{name: "pool_size", mutate: func(o *config_pb.RedisOption) { o.PoolSize = proto.Int32(-1) }},
		{name: "max_concurrent_dials", mutate: func(o *config_pb.RedisOption) { o.MaxConcurrentDials = proto.Int32(-1) }},
		{name: "min_idle_conns", mutate: func(o *config_pb.RedisOption) { o.MinIdleConns = proto.Int32(-1) }},
		{name: "max_idle_conns", mutate: func(o *config_pb.RedisOption) { o.MaxIdleConns = proto.Int32(-1) }},
		{name: "max_active_conns", mutate: func(o *config_pb.RedisOption) { o.MaxActiveConns = proto.Int32(-1) }},
		{name: "failing_timeout_seconds", mutate: func(o *config_pb.RedisOption) { o.FailingTimeoutSeconds = proto.Int32(-1) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			option := &config_pb.RedisOption{Addr: proto.String("127.0.0.1:6379")}
			tt.mutate(option)
			err := validateConnectionOption("cache", option)
			if err == nil || !strings.Contains(err.Error(), tt.name) {
				t.Fatalf("validateConnectionOption error = %v, want field %q", err, tt.name)
			}
		})
	}

	option := &config_pb.RedisOption{
		Addr:         proto.String("127.0.0.1:6379"),
		MinIdleConns: proto.Int32(3),
		MaxIdleConns: proto.Int32(2),
	}
	if err := validateConnectionOption("cache", option); err == nil ||
		!strings.Contains(err.Error(), "min_idle_conns exceeds max_idle_conns") {
		t.Fatalf("min/max idle validation error = %v", err)
	}
}

func TestValidateConnectionOptionChecksEveryDurationField(t *testing.T) {
	tests := []struct {
		name   string
		value  time.Duration
		mutate func(*config_pb.RedisOption, *durationpb.Duration)
	}{
		{name: "min_retry_backoff", value: -3, mutate: func(o *config_pb.RedisOption, d *durationpb.Duration) { o.MinRetryBackoff = d }},
		{name: "max_retry_backoff", value: -3, mutate: func(o *config_pb.RedisOption, d *durationpb.Duration) { o.MaxRetryBackoff = d }},
		{name: "dial_timeout", value: -1, mutate: func(o *config_pb.RedisOption, d *durationpb.Duration) { o.DialTimeout = d }},
		{name: "dialer_retry_timeout", value: -1, mutate: func(o *config_pb.RedisOption, d *durationpb.Duration) { o.DialerRetryTimeout = d }},
		{name: "read_timeout", value: -3, mutate: func(o *config_pb.RedisOption, d *durationpb.Duration) { o.ReadTimeout = d }},
		{name: "write_timeout", value: -3, mutate: func(o *config_pb.RedisOption, d *durationpb.Duration) { o.WriteTimeout = d }},
		{name: "pool_timeout", value: -1, mutate: func(o *config_pb.RedisOption, d *durationpb.Duration) { o.PoolTimeout = d }},
		{name: "conn_max_idle_time", value: -2, mutate: func(o *config_pb.RedisOption, d *durationpb.Duration) { o.ConnMaxIdleTime = d }},
		{name: "conn_max_lifetime", value: -1, mutate: func(o *config_pb.RedisOption, d *durationpb.Duration) { o.ConnMaxLifetime = d }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			option := &config_pb.RedisOption{Addr: proto.String("127.0.0.1:6379")}
			tt.mutate(option, durationpb.New(tt.value))
			err := validateConnectionOption("cache", option)
			if err == nil || !strings.Contains(err.Error(), tt.name) {
				t.Fatalf("validateConnectionOption error = %v, want field %q", err, tt.name)
			}
		})
	}
}

func TestValidateDurationAcceptsDocumentedSentinelsAndRejectsMalformedValues(t *testing.T) {
	for _, value := range []*durationpb.Duration{
		nil,
		durationpb.New(0),
		durationpb.New(5 * time.Second),
		durationpb.New(-1),
		durationpb.New(-2),
	} {
		if err := validateDuration("read_timeout", value, -1, -2); err != nil {
			t.Fatalf("valid duration %v rejected: %v", value, err)
		}
	}

	if err := validateDuration(
		"read_timeout",
		&durationpb.Duration{Nanos: 1_000_000_000},
		-1,
		-2,
	); err == nil || !strings.Contains(err.Error(), "is invalid") {
		t.Fatalf("malformed duration error = %v", err)
	}
	if err := validateDuration("read_timeout", durationpb.New(-3), -1, -2); err == nil ||
		!strings.Contains(err.Error(), "unsupported negative value") {
		t.Fatalf("unsupported duration error = %v", err)
	}
}
