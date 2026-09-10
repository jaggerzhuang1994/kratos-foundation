package redis

import (
	"context"
	"errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	foundationmetrics "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/protobuf/proto"
)

type localTracingProvider struct {
	provider trace.TracerProvider
	disabled bool
}

func (p localTracingProvider) Disabled() bool { return p.disabled }

func (p localTracingProvider) TracerProvider() trace.TracerProvider {
	return p.provider
}

func (p localTracingProvider) Tracer(
	name string,
	options ...trace.TracerOption,
) trace.Tracer {
	return p.provider.Tracer(name, options...)
}

type localMetricsProvider struct {
	provider metric.MeterProvider
	registry *prometheus.Registry
}

func newLocalMetricsProvider() foundationmetrics.Provider {
	return &localMetricsProvider{
		provider: metricnoop.NewMeterProvider(),
		registry: prometheus.NewRegistry(),
	}
}

func (p *localMetricsProvider) Meter(
	name string,
	options ...metric.MeterOption,
) metric.Meter {
	return p.provider.Meter(name, options...)
}

func (p *localMetricsProvider) MeterProvider() metric.MeterProvider {
	return p.provider
}

func (p *localMetricsProvider) PrometheusGatherer() prometheus.Gatherer {
	return p.registry
}

func (p *localMetricsProvider) PrometheusRegisterer() prometheus.Registerer {
	return p.registry
}

func newRedisTestLogger(t testing.TB) foundationlog.Logger {
	t.Helper()
	shared, cleanup, err := testlog.New(testlog.Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: testlog.FileConfig{OutputConfig: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		}},
	})
	if err != nil {
		t.Fatalf("create test logger: %v", err)
	}
	t.Cleanup(cleanup)
	return shared
}

func validRedisConfig() *config_pb.Redis {
	disabled := true
	return &config_pb.Redis{
		Default: proto.String("default"),
		Connections: map[string]*config_pb.RedisOption{
			"default": {Addr: proto.String("127.0.0.1:6379")},
		},
		Tracing: &config_pb.RedisTracing{Disable: &disabled},
		Metrics: &config_pb.RedisMetrics{Disable: &disabled},
	}
}

func localRedisDependencies(t testing.TB) (
	foundationlog.Logger,
	localTracingProvider,
	foundationmetrics.Provider,
) {
	t.Helper()
	return newRedisTestLogger(t),
		localTracingProvider{
			provider: tracenoop.NewTracerProvider(),
			disabled: true,
		},
		newLocalMetricsProvider()
}

func TestNewBuildsCachedClientsAndCleanupOwnsTheirLifecycle(t *testing.T) {
	config := validRedisConfig()
	config.Connections["cache"] = &config_pb.RedisOption{
		Addr:       proto.String("127.0.0.1:6380"),
		ClientName: proto.String("cache-client"),
	}
	configManager := testconfig.New(t, "redis", config)
	logger, tracingProvider, metricsProvider := localRedisDependencies(t)

	manager, cleanup, err := NewManager(
		logger,
		configManager,
		tracingProvider,
		metricsProvider,
	)
	if err != nil {
		t.Fatal(err)
	}
	defaultClient := manager.Default()
	if defaultClient == nil || defaultClient.Options().Addr != "127.0.0.1:6379" {
		t.Fatalf("default client = %#v", defaultClient)
	}
	cacheClient, err := manager.Connection("cache")
	if err != nil {
		t.Fatal(err)
	}
	again, err := manager.Connection("cache")
	if err != nil || again != cacheClient {
		t.Fatalf("cached Connection = (%p, %v), want %p", again, err, cacheClient)
	}
	if cacheClient.Options().Addr != "127.0.0.1:6380" ||
		cacheClient.Options().ClientName != "cache-client" {
		t.Fatalf("cache client options = %#v", cacheClient.Options())
	}
	if _, err := manager.Connection("unknown"); err == nil ||
		!strings.Contains(err.Error(), `connection "unknown" option is nil`) {
		t.Fatalf("unknown Connection error = %v", err)
	}

	cleanup()
	cleanup()
	if manager.Default() != nil {
		t.Fatal("cleanup left default client visible")
	}
	if _, err := manager.Connection("cache"); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Connection after cleanup error = %v, want ErrManagerClosed", err)
	}
	assertRedisClientClosed(t, defaultClient)
	assertRedisClientClosed(t, cacheClient)
}

func TestNewKeepsRestartOnlyStartupSnapshotAcrossConfigUpdates(t *testing.T) {
	initial := validRedisConfig()
	initial.Connections["cache"] = &config_pb.RedisOption{
		Addr: proto.String("127.0.0.1:6380"),
	}
	source := testconfig.NewMutableSource(t, "redis", initial)
	configManager, releaseConfig, err := foundationconfig.NewManager(
		foundationconfig.Sources{source},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(releaseConfig)

	updates := make(chan *config_pb.Redis, 2)
	cancelObserver, err := configManager.Subscribe(
		"redis",
		new(config_pb.Redis),
		func(_ string, value any, updateErr error) {
			if updateErr == nil {
				updates <- value.(*config_pb.Redis)
			}
		},
		defaultConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancelObserver)
	receiveRedisConfig(t, updates)

	logger, tracingProvider, metricsProvider := localRedisDependencies(t)
	manager, cleanup, err := NewManager(
		logger,
		configManager,
		tracingProvider,
		metricsProvider,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	next := proto.CloneOf(initial)
	next.Connections["cache"].Addr = proto.String("127.0.0.1:6381")
	source.Update(t, next)
	if updated := receiveRedisConfig(t, updates); updated.GetConnections()["cache"].GetAddr() != "127.0.0.1:6381" {
		t.Fatalf("config manager update = %#v", updated)
	}

	cacheClient, err := manager.Connection("cache")
	if err != nil {
		t.Fatal(err)
	}
	if cacheClient.Options().Addr != "127.0.0.1:6380" {
		t.Fatalf(
			"restart-only manager used updated address %q, want startup address",
			cacheClient.Options().Addr,
		)
	}
}

func TestNewPreservesConfigLoadFailureWithoutReturningResources(t *testing.T) {
	cause := errors.New("redis config source failed")
	logger, tracingProvider, metricsProvider := localRedisDependencies(t)

	manager, cleanup, err := NewManager(
		logger,
		loadErrorConfigManager{err: cause},
		tracingProvider,
		metricsProvider,
	)
	if !errors.Is(err, cause) {
		t.Fatalf("New error = %v, want config source cause", err)
	}
	if manager != nil || cleanup != nil {
		t.Fatalf(
			"New returned resources after config load failure: manager=%v cleanupPresent=%t",
			manager,
			cleanup != nil,
		)
	}
}

func TestNewRejectsLoggerPolicyBeforeReturningResources(t *testing.T) {
	config := validRedisConfig()
	config.Log = &config_pb.ModuleLog{FilterKeys: []string{""}}
	configManager := testconfig.New(t, "redis", config)
	logger, tracingProvider, metricsProvider := localRedisDependencies(t)

	manager, cleanup, err := NewManager(
		logger,
		configManager,
		tracingProvider,
		metricsProvider,
	)
	if err == nil || !strings.Contains(err.Error(), "configure redis logger") {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf(
			"New invalid logger policy: err=%v manager=%v cleanupPresent=%t",
			err,
			manager,
			cleanup != nil,
		)
	}
	if manager != nil || cleanup != nil {
		t.Fatalf(
			"New returned resources after logger failure: manager=%v cleanupPresent=%t",
			manager,
			cleanup != nil,
		)
	}
}

func TestNewInstallsEnabledTelemetryWithoutDialing(t *testing.T) {
	config := validRedisConfig()
	config.Tracing.Disable = proto.Bool(false)
	config.Metrics.Disable = proto.Bool(false)
	configManager := testconfig.New(t, "redis", config)
	logger, _, metricsProvider := localRedisDependencies(t)
	tracingProvider := localTracingProvider{
		provider: tracenoop.NewTracerProvider(),
		disabled: false,
	}

	manager, cleanup, err := NewManager(
		logger,
		configManager,
		tracingProvider,
		metricsProvider,
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if manager.Default() != nil {
		t.Fatal("cleanup left instrumented default client visible")
	}
}

func TestNewCleanupToleratesClientAlreadyClosedByCaller(t *testing.T) {
	configManager := testconfig.New(t, "redis", validRedisConfig())
	logger, tracingProvider, metricsProvider := localRedisDependencies(t)
	manager, cleanup, err := NewManager(
		logger,
		configManager,
		tracingProvider,
		metricsProvider,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Default().Close(); err != nil {
		t.Fatal(err)
	}

	cleanup()
	cleanup()
	if manager.Default() != nil {
		t.Fatal("cleanup with a preclosed client left the manager open")
	}
	if _, err := manager.Connection("default"); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Connection after cleanup error = %v, want ErrManagerClosed", err)
	}
}

func receiveRedisConfig(
	t testing.TB,
	updates <-chan *config_pb.Redis,
) *config_pb.Redis {
	t.Helper()
	select {
	case value := <-updates:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Redis config update")
		return nil
	}
}

func assertRedisClientClosed(t testing.TB, client interface {
	Ping(context.Context) *goredis.StatusCmd
}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := client.Ping(ctx).Err(); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("managed Redis client remained usable after cleanup: %v", err)
	}
}
