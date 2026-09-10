package redis

import (
	"context"
	"errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type disabledTracingProvider struct{ provider trace.TracerProvider }

func (disabledTracingProvider) Disabled() bool { return true }
func (p disabledTracingProvider) TracerProvider() trace.TracerProvider {
	return p.provider
}
func (p disabledTracingProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return p.provider.Tracer(name, options...)
}

func TestNewManagerBuildsCachesAndCleansUpConfiguredClients(t *testing.T) {
	loggerState, releaseLogger, err := testlog.New(testlog.Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std:        testlog.OutputConfig{Disable: true, Level: kratoslog.LevelInfo},
		File: testlog.FileConfig{OutputConfig: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(releaseLogger)
	metricProvider, releaseMetrics, err := metrics.NewProvider(appinfo.New("redis-test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(releaseMetrics)
	disabled := true
	configManager := testconfig.New(t, "redis", &config_pb.Redis{
		Default: proto.String("default"),
		Connections: map[string]*config_pb.RedisOption{
			"default": {Addr: proto.String("127.0.0.1:6379")},
			"cache":   {Addr: proto.String("127.0.0.1:6380")},
		},
		Tracing: &config_pb.RedisTracing{Disable: &disabled},
		Metrics: &config_pb.RedisMetrics{Disable: &disabled},
	})

	manager, cleanup, err := NewManager(
		loggerState,
		configManager,
		disabledTracingProvider{provider: noop.NewTracerProvider()},
		metricProvider,
	)
	if err != nil {
		t.Fatal(err)
	}
	defaultClient := manager.Default()
	if defaultClient == nil || defaultClient.Options().Addr != "127.0.0.1:6379" {
		t.Fatalf("default client = %#v", defaultClient)
	}
	cacheOne, err := manager.Connection("cache")
	if err != nil {
		t.Fatal(err)
	}
	cacheTwo, err := manager.Connection("cache")
	if err != nil || cacheTwo != cacheOne || cacheOne.Options().Addr != "127.0.0.1:6380" {
		t.Fatalf("cached client = (%p, %p, %v)", cacheOne, cacheTwo, err)
	}

	cleanup()
	cleanup()
	if manager.Default() != nil {
		t.Fatal("cleanup left the default client visible")
	}
	if _, err := manager.Connection("cache"); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Connection after cleanup error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := defaultClient.Ping(ctx).Err(); err == nil {
		t.Fatal("cleanup left the default client usable")
	}
}

func newLocalTestManager(options map[string]connectionOption) *manager {
	return &manager{
		conf: &config_pb.Redis{
			Tracing: &config_pb.RedisTracing{Disable: proto.Bool(true)},
			Metrics: &config_pb.RedisMetrics{Disable: proto.Bool(true)},
		},
		connOptions: options,
		connections: make(map[string]*goredis.Client),
	}
}

func localConnectionOption(address string) connectionOption {
	return &config_pb.RedisOption{
		Network:    proto.String("tcp"),
		Addr:       proto.String(address),
		ClientName: proto.String("test-client"),
		Protocol:   proto.Int32(3),
		Db:         proto.Int32(2),
		PoolSize:   proto.Int32(4),
	}
}

func TestManagerConnectionCachesRealClientAndCloseIsIdempotent(t *testing.T) {
	manager := newLocalTestManager(map[string]connectionOption{
		"cache": localConnectionOption("127.0.0.1:1"),
	})

	clients := make(chan *goredis.Client, 16)
	errorsByCall := make(chan error, 16)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			client, err := manager.Connection("cache")
			clients <- client
			errorsByCall <- err
		})
	}
	wg.Wait()
	close(clients)
	close(errorsByCall)
	for err := range errorsByCall {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first *goredis.Client
	for client := range clients {
		if first == nil {
			first = client
			continue
		}
		if client != first {
			t.Fatal("concurrent Connection calls returned different cached clients")
		}
	}
	manager.defaultConn = first
	if manager.Default() != first {
		t.Fatal("Default did not return the initialized client")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if manager.Default() != nil {
		t.Fatal("Default returned a client after Close")
	}
	if _, err := manager.Connection("cache"); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Connection after Close error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := first.Ping(ctx).Err(); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("managed client remained usable after Close: %v", err)
	}
}

func TestNewConnectionMapsOptionsWithoutDialing(t *testing.T) {
	option := &config_pb.RedisOption{
		Network:               proto.String("unix"),
		Addr:                  proto.String("/tmp/redis-provider-test.sock"),
		ClientName:            proto.String("test-client"),
		Protocol:              proto.Int32(2),
		Username:              proto.String("test-user"),
		Password:              proto.String("test-password"),
		Db:                    proto.Int32(2),
		MaxRetries:            proto.Int32(7),
		MinRetryBackoff:       durationpb.New(11 * time.Millisecond),
		MaxRetryBackoff:       durationpb.New(23 * time.Millisecond),
		DialTimeout:           durationpb.New(31 * time.Millisecond),
		DialerRetries:         proto.Int32(3),
		DialerRetryTimeout:    durationpb.New(37 * time.Millisecond),
		ReadTimeout:           durationpb.New(41 * time.Millisecond),
		WriteTimeout:          durationpb.New(43 * time.Millisecond),
		ContextTimeoutEnabled: proto.Bool(true),
		ReadBufferSize:        proto.Int32(4096),
		WriteBufferSize:       proto.Int32(8192),
		PoolFifo:              proto.Bool(true),
		PoolSize:              proto.Int32(10),
		MaxConcurrentDials:    proto.Int32(6),
		PoolTimeout:           durationpb.New(47 * time.Millisecond),
		MinIdleConns:          proto.Int32(2),
		MaxIdleConns:          proto.Int32(5),
		MaxActiveConns:        proto.Int32(9),
		ConnMaxIdleTime:       durationpb.New(53 * time.Millisecond),
		ConnMaxLifetime:       durationpb.New(59 * time.Millisecond),
		DisableIdentity:       proto.Bool(true),
		IdentitySuffix:        proto.String("test-suffix"),
		UnstableResp3:         proto.Bool(true),
		FailingTimeoutSeconds: proto.Int32(61),
	}
	manager := newLocalTestManager(map[string]connectionOption{
		"configured": option,
	})
	client, err := manager.newConnection("configured")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	options := client.Options()
	fields := []struct {
		name string
		got  any
		want any
	}{
		{name: "Network", got: options.Network, want: "unix"},
		{name: "Addr", got: options.Addr, want: "/tmp/redis-provider-test.sock"},
		{name: "ClientName", got: options.ClientName, want: "test-client"},
		{name: "Protocol", got: options.Protocol, want: 2},
		{name: "Username", got: options.Username, want: "test-user"},
		{name: "Password", got: options.Password, want: "test-password"},
		{name: "DB", got: options.DB, want: 2},
		{name: "MaxRetries", got: options.MaxRetries, want: 7},
		{name: "MinRetryBackoff", got: options.MinRetryBackoff, want: 11 * time.Millisecond},
		{name: "MaxRetryBackoff", got: options.MaxRetryBackoff, want: 23 * time.Millisecond},
		{name: "DialTimeout", got: options.DialTimeout, want: 31 * time.Millisecond},
		{name: "DialerRetries", got: options.DialerRetries, want: 1},
		{name: "DialerRetryTimeout", got: options.DialerRetryTimeout, want: 37 * time.Millisecond},
		{name: "ReadTimeout", got: options.ReadTimeout, want: 41 * time.Millisecond},
		{name: "WriteTimeout", got: options.WriteTimeout, want: 43 * time.Millisecond},
		{name: "ContextTimeoutEnabled", got: options.ContextTimeoutEnabled, want: true},
		{name: "ReadBufferSize", got: options.ReadBufferSize, want: 4096},
		{name: "WriteBufferSize", got: options.WriteBufferSize, want: 8192},
		{name: "PoolFIFO", got: options.PoolFIFO, want: true},
		{name: "PoolSize", got: options.PoolSize, want: 10},
		{name: "MaxConcurrentDials", got: options.MaxConcurrentDials, want: 6},
		{name: "PoolTimeout", got: options.PoolTimeout, want: 47 * time.Millisecond},
		{name: "MinIdleConns", got: options.MinIdleConns, want: 2},
		{name: "MaxIdleConns", got: options.MaxIdleConns, want: 5},
		{name: "MaxActiveConns", got: options.MaxActiveConns, want: 9},
		{name: "ConnMaxIdleTime", got: options.ConnMaxIdleTime, want: 53 * time.Millisecond},
		{name: "ConnMaxLifetime", got: options.ConnMaxLifetime, want: 59 * time.Millisecond},
		{name: "DisableIdentity", got: options.DisableIdentity, want: true},
		{name: "IdentitySuffix", got: options.IdentitySuffix, want: "test-suffix"},
		{name: "UnstableResp3", got: options.UnstableResp3, want: true},
		{name: "FailingTimeoutSeconds", got: options.FailingTimeoutSeconds, want: 61},
	}
	for _, field := range fields {
		if !reflect.DeepEqual(field.got, field.want) {
			t.Errorf("Redis option %s = %#v, want %#v", field.name, field.got, field.want)
		}
	}
	if _, err := manager.newConnection("missing"); err == nil || !strings.Contains(err.Error(), "option is nil") {
		t.Fatalf("missing connection error = %v", err)
	}
}

func TestInitializeManagerRollsBackInvalidDefault(t *testing.T) {
	if err := initializeManager(nil, "default"); err == nil {
		t.Fatal("nil manager accepted")
	}
	prepared := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	manager := newLocalTestManager(nil)
	manager.connections["prepared"] = prepared
	err := initializeManager(manager, "missing")
	if err == nil || !strings.Contains(err.Error(), "option is nil") || !manager.closed {
		t.Fatalf("initialize error = %v, closed = %t", err, manager.closed)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if pingErr := prepared.Ping(ctx).Err(); pingErr == nil || !strings.Contains(pingErr.Error(), "closed") {
		t.Fatalf("failed initialization did not close prepared client: %v", pingErr)
	}
}

func TestInitializeManagerCreatesMissingConnectionMap(t *testing.T) {
	manager := newLocalTestManager(map[string]connectionOption{
		"default": localConnectionOption("127.0.0.1:6379"),
	})
	manager.connections = nil
	if err := initializeManager(manager, "default"); err != nil {
		t.Fatal(err)
	}
	if manager.Default() == nil || len(manager.connections) != 1 {
		t.Fatalf("initialized manager = %#v", manager)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerNilReceiversReturnLifecycleErrors(t *testing.T) {
	var manager *manager
	if _, err := manager.Connection("default"); err == nil || !strings.Contains(err.Error(), "manager is nil") {
		t.Fatalf("nil Connection error = %v", err)
	}
	if manager.Default() != nil {
		t.Fatal("nil Default returned a client")
	}
	if err := manager.Close(); err == nil || !strings.Contains(err.Error(), "manager is nil") {
		t.Fatalf("nil Close error = %v", err)
	}
}

func TestManagerCloseAggregatesRealClientCloseFailures(t *testing.T) {
	alpha := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	zeta := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:2"})
	if err := alpha.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zeta.Close(); err != nil {
		t.Fatal(err)
	}
	manager := &manager{
		connections: map[string]*goredis.Client{
			"zeta":  zeta,
			"alpha": alpha,
		},
		defaultConn: alpha,
	}

	err := manager.Close()
	if err == nil || !strings.Contains(err.Error(), `close redis connection "alpha"`) ||
		!strings.Contains(err.Error(), `close redis connection "zeta"`) {
		t.Fatalf("aggregated Close error = %v", err)
	}
	if manager.Close() != err {
		t.Fatal("repeated Close did not return the stored aggregate")
	}
}

func TestWrapRedisCloseErrorPreservesCauseAndName(t *testing.T) {
	want := errors.New("close failed")
	if wrapRedisCloseError("cache", nil) != nil {
		t.Fatal("nil close error was wrapped")
	}
	got := wrapRedisCloseError("cache", want)
	if !errors.Is(got, want) || !strings.Contains(got.Error(), "cache") {
		t.Fatalf("wrapped close error = %v", got)
	}
}
