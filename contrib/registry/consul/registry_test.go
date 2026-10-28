package consul

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/registry"
	"github.com/hashicorp/consul/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

func TestRegistrationKeepsChecksTagsAndIndependentSnapshot(t *testing.T) {
	r := &registrar{config: registryConfig{healthCheckIntervalSeconds: 3, deregisterCriticalAfterSeconds: 60, tags: []string{"blue"}}}
	service := &registry.ServiceInstance{ID: "one", Name: "app", Version: "v1", Metadata: map[string]string{"zone": "a"}, Endpoints: []string{"http://127.0.0.1:8080", "grpc://[::1]:9090"}}
	payload, err := r.serviceRegistration(service)
	if err != nil {
		t.Fatal(err)
	}
	service.Metadata["zone"] = "changed"
	r.config.tags[0] = "changed"
	if payload.Meta["zone"] != "a" || payload.Tags[0] != "version=v1" || payload.Tags[1] != "blue" {
		t.Fatalf("registration snapshot=%+v", payload)
	}
	if payload.Address != "127.0.0.1" || payload.Port != 8080 || payload.TaggedAddresses["grpc"].Port != 9090 {
		t.Fatalf("endpoint mapping=%+v", payload)
	}
	if len(payload.Checks) != 3 || payload.Checks[0].TCP != "127.0.0.1:8080" || payload.Checks[0].Interval != "3s" || payload.Checks[0].Timeout != "5s" || payload.Checks[0].DeregisterCriticalServiceAfter != "60s" || payload.Checks[2].TTL != "6s" || payload.Checks[2].CheckID != "service:one" || payload.Checks[2].DeregisterCriticalServiceAfter != "60s" {
		t.Fatalf("checks=%+v", payload.Checks)
	}
	// gRPC 端点使用 grpc.health.v1 检查（跟随应用就绪），启用心跳时不参与自动注销
	if grpcCheck := payload.Checks[1]; grpcCheck.GRPC != "[::1]:9090" || grpcCheck.TCP != "" || grpcCheck.Interval != "3s" || grpcCheck.Timeout != "5s" || grpcCheck.DeregisterCriticalServiceAfter != "" {
		t.Fatalf("grpc check=%+v", grpcCheck)
	}
	r.config.disableHeartbeat = true
	r.config.disableHealthCheck = true
	payload, err = r.serviceRegistration(service)
	if err != nil || len(payload.Checks) != 0 {
		t.Fatalf("disabled checks=%v,%v", payload.Checks, err)
	}
	service.Endpoints = []string{"http://host:bad-port"}
	if _, err = r.serviceRegistration(service); err == nil {
		t.Fatal("invalid endpoint accepted")
	}
}

func TestRegistrationRejectsOverflowPort(t *testing.T) {
	r := &registrar{}
	for _, endpoint := range []string{"http://host:65536", "grpc://host:70000"} {
		if _, err := r.serviceRegistration(&registry.ServiceInstance{Endpoints: []string{endpoint}}); err == nil {
			t.Fatalf("accepted endpoint %q", endpoint)
		}
	}
	for _, endpoint := range []string{"http://host:65535", "http://host", "grpc://host:0"} {
		if _, err := r.serviceRegistration(&registry.ServiceInstance{Endpoints: []string{endpoint}}); err != nil {
			t.Fatalf("existing endpoint %q rejected: %v", endpoint, err)
		}
	}
}

func TestRegistrationGRPCHealthCheckOptions(t *testing.T) {
	service := &registry.ServiceInstance{ID: "one", Name: "app", Endpoints: []string{"http://10.0.0.1:8000", "grpc://10.0.0.1:9000"}}
	base := registryConfig{healthCheckIntervalSeconds: 5, deregisterCriticalAfterSeconds: 600, failuresBeforeCritical: 2}
	tests := []struct {
		name   string
		mutate func(*registryConfig)
		check  func(*testing.T, []*api.AgentServiceCheck)
	}{
		{"failures apply to endpoint checks but not ttl", func(*registryConfig) {}, func(t *testing.T, checks []*api.AgentServiceCheck) {
			if len(checks) != 3 || checks[0].FailuresBeforeCritical != 2 || checks[1].FailuresBeforeCritical != 2 || checks[2].TTL == "" || checks[2].FailuresBeforeCritical != 0 {
				t.Fatalf("checks=%+v", checks)
			}
		}},
		{"disable grpc health check falls back to tcp", func(c *registryConfig) { c.disableGRPCHealthCheck = true }, func(t *testing.T, checks []*api.AgentServiceCheck) {
			if checks[1].GRPC != "" || checks[1].TCP != "10.0.0.1:9000" || checks[1].DeregisterCriticalServiceAfter != "600s" {
				t.Fatalf("grpc endpoint check=%+v", checks[1])
			}
		}},
		{"without heartbeat grpc check deregisters", func(c *registryConfig) { c.disableHeartbeat = true }, func(t *testing.T, checks []*api.AgentServiceCheck) {
			if len(checks) != 2 || checks[1].GRPC != "10.0.0.1:9000" || checks[1].DeregisterCriticalServiceAfter != "600s" {
				t.Fatalf("checks=%+v", checks)
			}
		}},
		{"disable health check keeps only heartbeat", func(c *registryConfig) { c.disableHealthCheck = true }, func(t *testing.T, checks []*api.AgentServiceCheck) {
			if len(checks) != 1 || checks[0].TTL != "10s" {
				t.Fatalf("checks=%+v", checks)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := base
			tt.mutate(&config)
			payload, err := (&registrar{config: config}).serviceRegistration(service)
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, payload.Checks)
		})
	}
}

// 真实 Consul 中的 gRPC 健康检查：实例未就绪时变为 critical 并被发现过滤，但不会被注销；恢复后重新可见。
// Consul 运行在容器中，需要通过 FOUNDATION_TEST_CONSUL_CHECK_HOST 访问本测试启动的 gRPC 服务。
func TestExternalConsulGRPCHealthFollowsReadiness(t *testing.T) {
	address := os.Getenv("FOUNDATION_TEST_CONSUL_ADDR")
	checkHost := os.Getenv("FOUNDATION_TEST_CONSUL_CHECK_HOST")
	if address == "" || checkHost == "" {
		t.Skip("set FOUNDATION_TEST_CONSUL_ADDR and FOUNDATION_TEST_CONSUL_CHECK_HOST for Docker integration tests")
	}
	listen := os.Getenv("FOUNDATION_TEST_CONSUL_CHECK_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	readiness := health.NewServer()
	healthpb.RegisterHealthServer(server, readiness)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	client, err := api.NewClient(&api.Config{Address: address})
	if err != nil {
		t.Fatal(err)
	}
	options := &config_pb.RegistrarOptions{HealthcheckInternal: durationpb.New(time.Second), FailuresBeforeCritical: proto.Int32(2)}
	r, err := newRegistrar(testLogger(t), testPolicyConfig(t, "registry", options), client)
	if err != nil {
		t.Fatal(err)
	}
	d, err := newDiscovery(testLogger(t), testconfig.Empty(t), client)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("foundation-test-%d", time.Now().UnixNano())
	instance := &registry.ServiceInstance{ID: name + "-1", Name: name, Endpoints: []string{"grpc://" + net.JoinHostPort(checkHost, port)}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err = r.Register(ctx, instance); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.Deregister(ctx, instance); err != nil {
			t.Error(err)
		}
	})

	waitDiscovered := func(want int) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			// GetService 在没有健康实例时返回错误，这里直接查询健康实例列表
			instances, _, err := d.(*discovery).query(ctx, name, nil)
			if err == nil && len(instances) == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("discovered %d instances (err=%v), want %d", len(instances), err, want)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	// 新检查初始为 critical，首次检查通过后才可被发现
	waitDiscovered(1)

	readiness.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	waitDiscovered(0)
	services, err := client.Agent().Services()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := services[instance.ID]; !ok {
		t.Fatal("not ready instance was deregistered")
	}

	readiness.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	waitDiscovered(1)
}
