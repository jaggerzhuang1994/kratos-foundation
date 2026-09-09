package consul

import (
	"github.com/go-kratos/kratos/v2/registry"
	"testing"
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
	if len(payload.Checks) != 3 || payload.Checks[0].TCP != "127.0.0.1:8080" || payload.Checks[1].TCP != "[::1]:9090" || payload.Checks[0].Interval != "3s" || payload.Checks[0].Timeout != "5s" || payload.Checks[2].TTL != "6s" || payload.Checks[2].CheckID != "service:one" || payload.Checks[2].DeregisterCriticalServiceAfter != "60s" {
		t.Fatalf("checks=%+v", payload.Checks)
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
