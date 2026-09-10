package consul

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/registry"
	"github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestMissingHeartbeatReRegistersSnapshotBeforeContinuing(t *testing.T) {
	var registrations, updates atomic.Int32
	restored := make(chan api.AgentServiceRegistration, 1)
	healthy := make(chan struct{}, 1)
	reg := newTestRegistrar(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/service/register"):
			count := registrations.Add(1)
			if count == 2 {
				var payload api.AgentServiceRegistration
				_ = json.NewDecoder(r.Body).Decode(&payload)
				restored <- payload
			}
		case strings.Contains(r.URL.Path, "/check/update/"):
			count := updates.Add(1)
			if count == 1 {
				http.Error(w, "missing", http.StatusNotFound)
				return
			}
			if count == 2 {
				healthy <- struct{}{}
			}
		}
		w.WriteHeader(http.StatusOK)
	}, 0)
	service := &registry.ServiceInstance{ID: "node", Name: "app", Version: "v1"}
	if err := reg.Register(context.Background(), service); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Deregister(context.Background(), service) }()
	select {
	case <-healthy:
	case <-time.After(time.Second):
		t.Fatal("missing TTL was not re-registered and renewed")
	}
	// 重新登记沿用首次提交的身份和版本，不会重复创建不同实例。
	select {
	case payload := <-restored:
		if payload.ID != "node" || payload.Name != "app" {
			t.Fatalf("restored payload=%+v", payload)
		}
	default:
		t.Fatal("no registration after missing check")
	}
	if registrations.Load() != 2 {
		t.Fatalf("registrations=%d", registrations.Load())
	}
}

func TestHeartbeatPermissionFailureStopsWithoutDeregister(t *testing.T) {
	var updates, deregisters atomic.Int32
	reg := newTestRegistrar(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/check/update/") {
			updates.Add(1)
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		if strings.Contains(r.URL.Path, "/service/deregister/") {
			deregisters.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}, 0)
	service := &registry.ServiceInstance{ID: "node", Name: "app"}
	if err := reg.Register(context.Background(), service); err != nil {
		t.Fatal(err)
	}
	active := reg.(*registrar).services["node"]
	select {
	case <-active.done:
	case <-time.After(time.Second):
		t.Fatal("permanent error kept retrying")
	}
	if updates.Load() != 1 || deregisters.Load() != 0 {
		t.Fatalf("updates=%d deregisters=%d", updates.Load(), deregisters.Load())
	}
	if err := reg.Deregister(context.Background(), service); err != nil {
		t.Fatal(err)
	}
}

func TestDeregisterHTTPAndLifecycleQueueHonorCallerDeadline(t *testing.T) {
	entered := make(chan struct{}, 1)
	reg := newTestRegistrar(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/service/deregister/") {
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusOK)
	}, 0)
	service := &registry.ServiceInstance{ID: "node", Name: "app"}
	if err := reg.Register(context.Background(), service); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- reg.Deregister(ctx, service) }()
	<-entered
	queuedCtx, queuedCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer queuedCancel()
	if err := reg.Register(queuedCtx, service); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued Register deadline=%v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Deregister deadline=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Deregister ignored caller deadline")
	}
}

func newTestRegistrar(t *testing.T, handler http.HandlerFunc, timeout time.Duration) registry.Registrar {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(data))
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := api.NewClient(&api.Config{Address: server.URL, HttpClient: &http.Client{Timeout: timeout}})
	if err != nil {
		t.Fatal(err)
	}
	shared, cleanup, err := testlog.New(testlog.Config{TimeFormat: time.RFC3339, Std: testlog.OutputConfig{Disable: true}, File: testlog.FileConfig{OutputConfig: testlog.OutputConfig{Disable: true}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	reg, err := NewRegistry(shared, testconfig.New(t, "registry", &config_pb.Registry{HealthcheckInternal: durationpb.New(time.Second)}), client)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestHeartbeatRequestTimeoutRecovers(t *testing.T) {
	var updates, deregisters atomic.Int32
	recovered := make(chan struct{}, 1)
	reg := newTestRegistrar(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/check/update/") {
			switch updates.Add(1) {
			case 2:
				<-r.Context().Done()
				return
			case 3:
				recovered <- struct{}{}
			}
		}
		if strings.Contains(r.URL.Path, "/service/deregister/") {
			deregisters.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}, 40*time.Millisecond)
	service := &registry.ServiceInstance{ID: "node", Name: "app"}
	if err := reg.Register(context.Background(), service); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Deregister(context.Background(), service) }()
	select {
	case <-recovered:
	case <-time.After(2500 * time.Millisecond):
		t.Fatalf("heartbeat stopped after request timeout: updates=%d deregisters=%d", updates.Load(), deregisters.Load())
	}
	if deregisters.Load() != 0 {
		t.Fatal("request timeout must not deregister a live service")
	}
}

func TestDeregisterCancelsInitialHeartbeat(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	reg := newTestRegistrar(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/check/update/") {
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}
		w.WriteHeader(http.StatusOK)
	}, 0)
	service := &registry.ServiceInstance{ID: "node", Name: "app"}
	if err := reg.Register(context.Background(), service); err != nil {
		t.Fatal(err)
	}
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- reg.Deregister(ctx, service) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Deregister blocked on initial heartbeat without lifecycle context")
	}
}
