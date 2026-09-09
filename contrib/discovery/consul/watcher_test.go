package consul

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/registry"
	"github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
)

func TestWatchRecoversAfterEstablishedOutage(t *testing.T) {
	var stage atomic.Int32
	failed := make(chan struct{}, 8)
	d := newTestDiscovery(t, func(w http.ResponseWriter, r *http.Request) {
		switch stage.Load() {
		case 1:
			select {
			case failed <- struct{}{}:
			default:
			}
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case 2:
			writeServices(w, 2, false)
		default:
			writeServices(w, 1, true)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	watcher, err := d.Watch(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Stop() }()
	if _, err = watcher.Next(); err != nil {
		t.Fatal(err)
	}
	stage.Store(1)
	for range 3 {
		select {
		case <-failed:
		case <-ctx.Done():
			t.Fatal("watch did not keep retrying temporary outage")
		}
	}
	stage.Store(2)
	got, err := watcher.Next()
	if err != nil || len(got) != 0 {
		t.Fatalf("recovery=%v,%v", got, err)
	}
}

func TestWatchStopCancelsPendingHTTPAndIsIdempotent(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{})
	d := newTestDiscovery(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			writeServices(w, 1, false)
			return
		}
		close(entered)
		<-r.Context().Done()
	})
	watcher, err := d.Watch(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := watcher.Next(); err != nil || len(got) != 0 {
		t.Fatalf("initial empty=%v,%v", got, err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("watch did not begin query")
	}
	done := make(chan error, 1)
	go func() { done <- watcher.Stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel HTTP")
	}
	if err = watcher.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err = watcher.Next(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Next after Stop=%v", err)
	}
}

func TestWatchStopsOnPermissionDenied(t *testing.T) {
	var calls atomic.Int32
	d := newTestDiscovery(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			writeServices(w, 1, true)
			return
		}
		http.Error(w, "permission denied", http.StatusForbidden)
	})
	watcher, err := d.Watch(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Stop() }()
	for {
		if _, err = watcher.Next(); err != nil {
			break
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("permanent failure retried %d times", calls.Load())
	}
}

func TestWatchResetsBlockingIndexAfterConsulRollback(t *testing.T) {
	var calls atomic.Int32
	requested := make(chan string, 1)
	d := newTestDiscovery(t, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			writeServices(w, 9, true)
		case 2:
			writeServices(w, 3, false)
		default:
			select {
			case requested <- r.URL.Query().Get("index"):
			default:
			}
			writeServices(w, 3, false)
		}
	})
	watcher, err := d.Watch(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Stop() }()
	select {
	case index := <-requested:
		if index != "" {
			t.Fatalf("Consul index rollback must reset next WaitIndex=0; request index=%q", index)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no query following rollback")
	}
}

func newTestDiscovery(t *testing.T, handler http.HandlerFunc) registry.Discovery {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := api.NewClient(&api.Config{Address: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDiscovery(testLogger(t), testconfig.Empty(t), client)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func writeServices(w http.ResponseWriter, index int, present bool) {
	w.Header().Set("X-Consul-Index", fmt.Sprint(index))
	entries := []*api.ServiceEntry{}
	if present {
		entries = append(entries, &api.ServiceEntry{Service: &api.AgentService{ID: "node", Service: "app", Address: "127.0.0.1", Port: 8080}})
	}
	_ = json.NewEncoder(w).Encode(entries)
}

func TestWatchInitialFailureDoesNotPoisonNextWatch(t *testing.T) {
	var calls atomic.Int32
	d := newTestDiscovery(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		writeServices(w, 1, true)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := d.Watch(ctx, "app"); err == nil {
		t.Fatal("initial Watch must report the outage")
	}
	watcher, err := d.Watch(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Stop() }()
	got, err := watcher.Next()
	if err != nil || len(got) != 1 {
		t.Fatalf("recovered watch = %v, %v (requests=%d)", got, err, calls.Load())
	}
}

func TestWatchPublishesEmptyServiceList(t *testing.T) {
	var empty atomic.Bool
	d := newTestDiscovery(t, func(w http.ResponseWriter, r *http.Request) {
		if empty.Load() {
			writeServices(w, 2, false)
			return
		}
		writeServices(w, 1, true)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	watcher, err := d.Watch(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Stop() }()
	got, err := watcher.Next()
	if err != nil || len(got) != 1 {
		t.Fatalf("initial = %v, %v", got, err)
	}
	empty.Store(true)
	got, err = watcher.Next()
	if err != nil || len(got) != 0 {
		t.Fatalf("removed service must publish empty list: %v, %v", got, err)
	}
}
