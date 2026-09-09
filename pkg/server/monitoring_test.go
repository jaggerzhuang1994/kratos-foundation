package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/transport"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/prometheus/client_golang/prometheus"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/protobuf/proto"
)

func TestMonitoringListenerPlacementAndRouteIsolation(t *testing.T) {
	for _, tt := range []struct {
		name, metricsAddr, healthAddr string
		disabled                      bool
		extras                        int
		mainMetrics, mainHealth       bool
	}{
		{name: "default", mainMetrics: true, mainHealth: true},
		{name: "same business address", metricsAddr: "127.0.0.1:8000", healthAddr: "127.0.0.1:8000", mainMetrics: true, mainHealth: true},
		{name: "metrics separate", metricsAddr: "127.0.0.1:9001", extras: 1, mainHealth: true},
		{name: "health separate", healthAddr: "127.0.0.1:9001", extras: 1, mainMetrics: true},
		{name: "shared monitoring", metricsAddr: "127.0.0.1:9001", healthAddr: "127.0.0.1:9001", extras: 1},
		{name: "two monitoring listeners", metricsAddr: "127.0.0.1:9001", healthAddr: "127.0.0.1:9002", extras: 2},
		{name: "business disabled defaults", disabled: true},
		{name: "business disabled explicit", disabled: true, metricsAddr: "127.0.0.1:8000", healthAddr: "127.0.0.1:8000", extras: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := proto.CloneOf(defaultConfig)
			config.Http.Addr = proto.String("127.0.0.1:8000")
			config.Http.Metrics.Addr = proto.String(tt.metricsAddr)
			config.Http.Health.Addr = proto.String(tt.healthAddr)
			var business HTTPServer
			if !tt.disabled {
				business = kratoshttp.NewServer()
				business.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/orders" {
						w.WriteHeader(204)
					} else {
						w.WriteHeader(404)
					}
				})
			}
			health := configuredHealth(config, NewSpec())
			health.applicationReady = func() bool { return true }
			extras, err := configureMonitoring(config, business, health, testMetricsProvider{registry: prometheus.NewRegistry()})
			if err != nil || len(extras) != tt.extras {
				t.Fatalf("extras=%d err=%v", len(extras), err)
			}
			probe := func(srv HTTPServer, path string, expected bool) {
				t.Helper()
				response := httptest.NewRecorder()
				srv.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
				if (response.Code == 200) != expected {
					t.Fatalf("%s=%d expected mounted=%v", path, response.Code, expected)
				}
			}
			if business != nil {
				probe(business, "/metrics", tt.mainMetrics)
				probe(business, "/readyz", tt.mainHealth)
			}
			for i, srv := range extras {
				hasMetrics := tt.metricsAddr != "" && (i == 0)
				hasHealth := tt.healthAddr != "" && (tt.metricsAddr == "" || tt.metricsAddr == tt.healthAddr || i == 1)
				probe(srv, "/metrics", hasMetrics)
				probe(srv, "/readyz", hasHealth)
				probe(srv, "/orders", false)
			}
		})
	}
}

func TestMonitoringRejectsInvalidAddressesAndSharedPaths(t *testing.T) {
	for _, addr := range []string{"localhost", "http://localhost:9001", "host:-1", "host:65536", "host:http", " host:123", "bad host:123"} {
		if _, err := canonicalMonitoringAddr(addr); err == nil {
			t.Fatalf("accepted %q", addr)
		}
	}
	for _, pair := range [][2]string{{"0.0.0.0:08000", ":8000"}, {"[0:0:0:0:0:0:0:1]:9001", "[::1]:9001"}} {
		got, err := canonicalMonitoringAddr(pair[0])
		if err != nil || got != pair[1] {
			t.Fatalf("%q => %q %v", pair[0], got, err)
		}
	}
	config := proto.CloneOf(defaultConfig)
	health := configuredHealth(config, NewSpec())
	health.config.LivenessPath = "/metrics"
	business := kratoshttp.NewServer()
	provider := testMetricsProvider{registry: prometheus.NewRegistry()}
	if _, err := configureMonitoring(config, business, health, provider); err == nil {
		t.Fatal("shared path accepted")
	}
	health.config.Addr = "127.0.0.1:9001"
	if _, err := configureMonitoring(config, business, health, provider); err != nil {
		t.Fatalf("different listeners may use same path: %v", err)
	}
	for _, kind := range []string{"metrics", "health"} {
		config := proto.CloneOf(defaultConfig)
		health := configuredHealth(config, NewSpec())
		if kind == "metrics" {
			config.Http.Metrics.Addr = proto.String("invalid")
		} else {
			health.config.Addr = "invalid"
		}
		if _, err := configureMonitoring(config, business, health, provider); err == nil {
			t.Fatalf("invalid %s address accepted", kind)
		}
	}
	config.Http.Metrics.Path = proto.String("bad")
	if _, err := configureMonitoring(config, business, configuredHealth(config, NewSpec()), provider); err == nil {
		t.Fatal("invalid metrics path accepted")
	}
}

func TestManagementHTTPRunsAndStopsWithoutDiscoveryEndpoint(t *testing.T) {
	config := proto.CloneOf(defaultConfig)
	config.Http.Disable, config.Grpc.Disable = proto.Bool(true), proto.Bool(true)
	config.Http.Metrics.Addr = proto.String("127.0.0.1:0")
	config.Http.Health.Addr = proto.String("127.0.0.1:0")
	runtime, cleanup, err := NewRuntime(testconfig.New(t, "server", config), newRuntimeTestLogger(t), testMetricsProvider{registry: prometheus.NewRegistry()}, runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}, NewSpec())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	runtime.SetReadinessSource(func() bool { return true })
	servers := runtime.ManagementServers()
	if len(servers) != 1 {
		t.Fatalf("servers=%d", len(servers))
	}
	if _, ok := servers[0].(transport.Endpointer); ok {
		t.Fatal("management port exposed for service discovery")
	}
	endpoint, err := runtime.management[0].Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- servers[0].Start(context.Background()) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := servers[0].Stop(ctx); err != nil {
			t.Error(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-ctx.Done():
			t.Error("management server did not stop")
		}
	}()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		response, err := client.Get(endpoint.String() + path)
		if err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 {
			t.Fatalf("%s=%d", path, response.StatusCode)
		}
	}
}

func TestManagementBindFailureIsReturned(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := occupied.Close(); err != nil {
			t.Error(err)
		}
	}()
	config := proto.CloneOf(defaultConfig)
	config.Http.Metrics.Addr = proto.String(occupied.Addr().String())
	extras, err := configureMonitoring(config, nil, configuredHealth(config, NewSpec()), testMetricsProvider{registry: prometheus.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{management: extras, health: configuredHealth(config, NewSpec())}
	if err := runtime.ManagementServers()[0].Start(context.Background()); err == nil || !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("bind error=%v", err)
	}
}
