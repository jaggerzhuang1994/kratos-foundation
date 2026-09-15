package consul

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestGetServicePreservesSDKMappingAndErrors(t *testing.T) {
	d := newTestDiscovery(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health/service/missing" {
			writeServices(w, 1, false)
			return
		}
		if r.URL.Path == "/v1/health/service/denied" {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode([]*api.ServiceEntry{{Service: &api.AgentService{ID: "one", Service: "app", Tags: []string{"version=v1", "version=v2"}, Meta: map[string]string{"zone": "a"}, TaggedAddresses: map[string]api.ServiceAddress{"http": {Address: "http://host:80"}, "grpc": {Address: "grpc://host:90"}, "lan_ipv4": {Address: "127.0.0.1"}}}}})
	})
	services, err := d.GetService(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || services[0].Version != "v2" || services[0].Metadata["zone"] != "a" || len(services[0].Endpoints) != 2 {
		t.Fatalf("service mapping=%+v", services)
	}
	if _, err = d.GetService(context.Background(), "missing"); err == nil {
		t.Fatal("missing service succeeded")
	}
	if _, err = d.GetService(context.Background(), "denied"); err == nil {
		t.Fatal("permission error was lost")
	}
}

func TestMultiDatacenterQueriesDoNotMixBlockingIndices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/catalog/datacenters" {
			_, _ = w.Write([]byte(`["dc1","dc2"]`))
			return
		}
		if r.URL.Query().Get("index") != "" {
			t.Errorf("multi-DC request unexpectedly blocks: %s", r.URL)
		}
		writeServices(w, 3, true)
	}))
	defer server.Close()
	client, err := api.NewClient(&api.Config{Address: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	d, err := newDiscovery(testLogger(t), testPolicyConfig(t, "discovery", &config_pb.Discovery{Dc: config_pb.DC_MULTI.Enum(), Timeout: durationpb.New(time.Second)}), client)
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.GetService(context.Background(), "app")
	if err != nil || len(got) != 2 {
		t.Fatalf("multi-DC=%v,%v", got, err)
	}
	if got[0].Metadata["dc"] != "dc1" || got[1].Metadata["dc"] != "dc2" {
		t.Fatalf("DC metadata=%v,%v", got[0].Metadata, got[1].Metadata)
	}
}

func TestMultiDatacenterCatalogHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	client, err := api.NewClient(&api.Config{Address: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	d, err := newDiscovery(testLogger(t), testPolicyConfig(t, "discovery", &config_pb.Discovery{Dc: config_pb.DC_MULTI.Enum(), Timeout: durationpb.New(40 * time.Millisecond)}), client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Watch(context.Background(), "app"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("catalog deadline=%v", err)
	}
}
