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

	kratosconfig "github.com/go-kratos/kratos/v2/config"
	consulapi "github.com/hashicorp/consul/api"
)

func consulTestSource(t *testing.T, handler http.HandlerFunc) (kratosconfig.Source, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := consulapi.NewClient(&consulapi.Config{Address: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	sources, err := newSources(client, PathList{"settings/*"})
	if err != nil {
		t.Fatal(err)
	}
	return sources[0], server
}

func nextConsulValues(t *testing.T, watcher kratosconfig.Watcher) ([]*kratosconfig.KeyValue, error) {
	t.Helper()
	type result struct {
		values []*kratosconfig.KeyValue
		err    error
	}
	results := make(chan result, 1)
	go func() { values, err := watcher.Next(); results <- result{values, err} }()
	select {
	case got := <-results:
		return got.values, got.err
	case <-time.After(time.Second):
		t.Fatal("watcher failed to deliver state or error")
		return nil, context.DeadlineExceeded
	}
}

func TestWatcherIncludesPureDeletionAndEmptySnapshot(t *testing.T) {
	var requests atomic.Int32
	first := &consulapi.KVPair{Key: "settings/first.yaml", Value: []byte("first: true"), ModifyIndex: 1}
	second := &consulapi.KVPair{Key: "settings/second.yaml", Value: []byte("second: true"), ModifyIndex: 1}
	input, _ := consulTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		request := requests.Add(1)
		if request > 3 {
			<-r.Context().Done()
			return
		}
		w.Header().Set("X-Consul-Index", fmt.Sprint(request))
		values := consulapi.KVPairs{first, second}
		if request == 2 {
			values = consulapi.KVPairs{first}
		}
		if request == 3 {
			values = consulapi.KVPairs{}
		}
		_ = json.NewEncoder(w).Encode(values)
	})
	watcher, err := input.Watch()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watcher.Stop() })
	for _, count := range []int{2, 1, 0} {
		values, err := nextConsulValues(t, watcher)
		if err != nil || len(values) != count {
			t.Fatalf("snapshot = %v, %v; want %d entries", values, err, count)
		}
	}
}

func TestWatcherRecoversServerFailureAndReportsAuthorizationFailure(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests atomic.Int32
			input, _ := consulTestSource(t, func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) == 1 || status == http.StatusForbidden {
					http.Error(w, "unavailable", status)
					return
				}
				w.Header().Set("X-Consul-Index", "2")
				_ = json.NewEncoder(w).Encode(consulapi.KVPairs{{Key: "settings/value.yaml", Value: []byte("value: recovered"), ModifyIndex: 2}})
			})
			watcher, err := input.Watch()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = watcher.Stop() })
			values, err := nextConsulValues(t, watcher)
			if status == http.StatusForbidden {
				if err == nil || requests.Load() != 1 {
					t.Fatalf("authorization failure = %v, requests=%d", err, requests.Load())
				}
			} else if err != nil || len(values) != 1 {
				t.Fatalf("recovered snapshot = %v, %v", values, err)
			}
		})
	}
}

func TestWatcherStopCancelsBlockedQueryAndIsIndependent(t *testing.T) {
	started := make(chan struct{}, 2)
	input, _ := consulTestSource(t, func(_ http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	})
	first, _ := input.Watch()
	second, _ := input.Watch()
	t.Cleanup(func() { _ = first.Stop(); _ = second.Stop() })
	results := make(chan error, 1)
	go func() { _, err := first.Next(); results <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("query did not start")
	}
	if err := first.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := first.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-results:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stopped query = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel query")
	}
	if second.(*kvWatcher).ctx.Err() != nil {
		t.Fatal("Stop canceled another watcher")
	}
}

func TestWatcherStopCancelsRecoveryBackoff(t *testing.T) {
	requested := make(chan struct{}, 1)
	input, _ := consulTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		select {
		case requested <- struct{}{}:
		default:
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	})
	watcher, _ := input.Watch()
	done := make(chan error, 1)
	go func() { _, err := watcher.Next(); done <- err }()
	<-requested
	_ = watcher.Stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled recovery = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel recovery")
	}
}

func TestWatcherClampsZeroIndexAndResetsRolledBackIndex(t *testing.T) {
	indexes := []string{"0", "0", "7", "2", "3"}
	queries := []string{"", "1", "1", "7", ""}
	var requests atomic.Int32
	input, _ := consulTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		index := int(requests.Add(1)) - 1
		if index >= len(indexes) {
			t.Error("unexpected extra query")
			http.Error(w, "extra query", 400)
			return
		}
		if got := r.URL.Query().Get("index"); got != queries[index] {
			t.Errorf("query %d index=%q, want %q", index, got, queries[index])
		}
		w.Header().Set("X-Consul-Index", indexes[index])
		_ = json.NewEncoder(w).Encode(consulapi.KVPairs{})
	})
	watcher, _ := input.Watch()
	defer func() { _ = watcher.Stop() }()
	for range indexes {
		if _, err := nextConsulValues(t, watcher); err != nil {
			t.Fatal(err)
		}
	}
}
