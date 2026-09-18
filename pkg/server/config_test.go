package server

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestServerRejectsInvalidBBRConfiguration(t *testing.T) {
	for _, tt := range []struct {
		name    string
		limiter *config_pb.Middleware_RateLimit_BBRLimiter
	}{
		{"zero bucket", &config_pb.Middleware_RateLimit_BBRLimiter{Bucket: proto.Int32(0)}},
		{"negative bucket", &config_pb.Middleware_RateLimit_BBRLimiter{Bucket: proto.Int32(-1)}},
		{"zero window", &config_pb.Middleware_RateLimit_BBRLimiter{Window: durationpb.New(0)}},
		{"negative window", &config_pb.Middleware_RateLimit_BBRLimiter{Window: durationpb.New(-time.Second)}},
		{"invalid duration", &config_pb.Middleware_RateLimit_BBRLimiter{Window: &durationpb.Duration{Nanos: 1_000_000_000}}},
		{"overflow duration", &config_pb.Middleware_RateLimit_BBRLimiter{Window: &durationpb.Duration{Seconds: 10_000_000_000}}},
		{"zero bucket duration", &config_pb.Middleware_RateLimit_BBRLimiter{Window: durationpb.New(time.Nanosecond)}},
		{"zero buckets per second", &config_pb.Middleware_RateLimit_BBRLimiter{Window: durationpb.New(101 * time.Second)}},
		{"zero cpu threshold", &config_pb.Middleware_RateLimit_BBRLimiter{CpuThreshold: proto.Int64(0)}},
		{"negative cpu quota", &config_pb.Middleware_RateLimit_BBRLimiter{CpuQuota: proto.Float64(-1)}},
		{"nan cpu quota", &config_pb.Middleware_RateLimit_BBRLimiter{CpuQuota: proto.Float64(math.NaN())}},
		{"infinite cpu quota", &config_pb.Middleware_RateLimit_BBRLimiter{CpuQuota: proto.Float64(math.Inf(1))}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := &config_pb.Server{RateLimit: &config_pb.Middleware_RateLimit{Enable: proto.Bool(true), BbrLimiter: tt.limiter}}
			if err := validateMiddlewareConfig(config); err == nil {
				t.Fatal("enabled invalid BBR configuration was accepted")
			}
			config.RateLimit.Enable = proto.Bool(false)
			if err := validateMiddlewareConfig(config); err != nil {
				t.Fatalf("disabled BBR configuration must be ignored: %v", err)
			}
		})
	}
}

func TestNewRuntimeRejectsInvalidBBRBeforeConstruction(t *testing.T) {
	config := &config_pb.Server{RateLimit: &config_pb.Middleware_RateLimit{
		Enable: proto.Bool(true), BbrLimiter: &config_pb.Middleware_RateLimit_BBRLimiter{Bucket: proto.Int32(0)},
	}}
	defer func() {
		if value := recover(); value != nil {
			t.Fatalf("NewRuntime panicked instead of rejecting config: %v", value)
		}
	}()
	runtime, cleanup, err := NewRuntime(testconfig.New(t, "server", config), newRuntimeTestLogger(t),
		testMetricsProvider{registry: prometheus.NewRegistry()}, runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}, NewSpec())
	if cleanup != nil {
		cleanup()
	}
	if err == nil || runtime != nil {
		t.Fatalf("NewRuntime = (%v, %v), want configuration error", runtime, err)
	}
}

func TestLoadConfigReturnsIndependentDefaultSnapshots(t *testing.T) {
	manager := testconfig.Empty(t)
	first, err := loadConfig(manager)
	if err != nil {
		t.Fatal(err)
	}
	first.GetHttp().Addr = strp("127.0.0.1:1")

	second, err := loadConfig(manager)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := second.GetHttp().GetAddr(), "0.0.0.0:8000"; got != want {
		t.Fatalf("second default HTTP address = %q, want %q", got, want)
	}
}

func TestConfigValidationRejectsInvalidRuntimePolicy(t *testing.T) {
	manager := testconfig.Empty(t)
	badDelay, err := loadConfig(manager)
	if err != nil {
		t.Fatal(err)
	}
	badDelay.StopDelay = durationpb.New(-time.Second)
	if err := validateConfig(badDelay); err == nil {
		t.Fatal("negative stop delay was accepted")
	}
	badMetadata, err := loadConfig(manager)
	if err != nil {
		t.Fatal(err)
	}
	badMetadata.Metadata = &config_pb.Middleware_Metadata{Prefix: []string{" "}}
	if err := validateConfig(badMetadata); err == nil {
		t.Fatal("unsafe metadata prefix was accepted")
	}
}

func TestHealthChecksPreserveListenerConfiguration(t *testing.T) {
	config := proto.CloneOf(defaultConfig)
	config.Http.Health.Addr = proto.String("127.0.0.1:9001")
	config.Http.Health.ReadinessPath = proto.String("/ready")
	spec := NewSpec()
	spec.Health().Checks(ReadinessCheck{Name: "database", Check: func(context.Context) error { return nil }})
	health := configuredHealth(config, spec)
	if health.config.Addr != "127.0.0.1:9001" || health.config.ReadinessPath != "/ready" || len(health.config.Checks) != 1 {
		t.Fatalf("health=%+v", health.config)
	}
	config.Http.Health.Disable = proto.Bool(true)
	if !configuredHealth(config, spec).config.Disable {
		t.Fatal("code checks overrode deployment disable")
	}
	// 构造后的运行时持有检查切片副本，后续声明不会污染已构造快照。
	spec.Health().Checks(ReadinessCheck{Name: "cache", Check: func(context.Context) error { return nil }})
	if len(health.config.Checks) != 1 || len(configuredHealth(config, spec).config.Checks) != 2 {
		t.Fatal("health checks do not have independent snapshot ownership")
	}
}
