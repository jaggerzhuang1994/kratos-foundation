package consul

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/text"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	consulapi "github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

func TestNewSourcesReturnsOneSourcePerPath(t *testing.T) {
	client, err := consulapi.NewClient(consulapi.DefaultConfig())
	if err != nil {
		t.Fatalf("create Consul client: %v", err)
	}
	logger, releaseLogger, logErr := log.NewLogger()
	if logErr != nil {
		t.Fatal(logErr)
	}
	t.Cleanup(releaseLogger)
	paths := PathList{"service/base", "service/override"}

	sources, err := NewSources(client, logger, paths)
	if err != nil {
		t.Fatalf("NewSources() error = %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("NewSources() count = %d, want 2", len(sources))
	}
	for index, source := range sources {
		if source == nil {
			t.Errorf("source %d is nil", index)
		}
	}
}

func TestNewSourcesHandlesDisabledAndInvalidInputs(t *testing.T) {
	client, err := consulapi.NewClient(consulapi.DefaultConfig())
	if err != nil {
		t.Fatalf("create Consul client: %v", err)
	}
	logger, releaseLogger, logErr := log.NewLogger()
	if logErr != nil {
		t.Fatal(logErr)
	}
	t.Cleanup(releaseLogger)

	empty, err := NewSources(client, logger, nil)
	if err != nil || empty != nil {
		t.Fatalf("NewSources(empty) = %#v, %v; want nil, nil", empty, err)
	}
	disabled, err := NewSources(nil, logger, PathList{"service/config"})
	if err != nil || disabled != nil {
		t.Fatalf("NewSources(nil client) = %#v, %v; want nil, nil", disabled, err)
	}

	tests := []struct {
		name  string
		paths PathList
	}{
		{name: "empty path", paths: PathList{""}},
		{name: "surrounding whitespace", paths: PathList{" service/config"}},
		{name: "duplicate", paths: PathList{"service/config", "service/config"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewSources(client, logger, test.paths); err == nil {
				t.Fatal("NewSources() error = nil")
			}
		})
	}
}

type consulRoundTripFunc func(*http.Request) (*http.Response, error)

func (f consulRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLoadBoundsRemoteRequestAndPreservesError(t *testing.T) {
	called := false
	client, err := consulapi.NewClient(&consulapi.Config{HttpClient: &http.Client{Transport: consulRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > loadTimeout {
			t.Error("Load request lacks bounded deadline")
		}
		return nil, context.Canceled
	})}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = (&kvSource{client: client, path: "settings"}).Load()
	if !called || !errors.Is(err, context.Canceled) {
		t.Fatalf("Load called=%v err=%v", called, err)
	}
}

func TestLoadRetriesTemporaryResponseAndDecodesWholePrefix(t *testing.T) {
	var requests atomic.Int32
	input, _ := consulTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "busy", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("X-Consul-Index", "3")
		_ = json.NewEncoder(w).Encode(consulapi.KVPairs{
			{Key: "settings/", Value: []byte("ignored")},
			{Key: "settings/value.yaml", Value: []byte("value: true")},
		})
	})
	values, err := input.Load()
	if err != nil || len(values) != 1 || values[0].Key != "value.yaml" || values[0].Format != "yaml" || requests.Load() != 2 {
		t.Fatalf("Load=%v err=%v requests=%d", values, err, requests.Load())
	}
}

func TestConsulRetryClassification(t *testing.T) {
	for _, test := range []struct {
		err   error
		retry bool
	}{
		{io.EOF, true}, {context.Canceled, false},
		{consulapi.StatusError{Code: http.StatusServiceUnavailable}, true},
		{consulapi.StatusError{Code: http.StatusUnauthorized}, false},
		{fmt.Errorf("wrapped: %w", consulapi.StatusError{Code: http.StatusForbidden}), false},
	} {
		if got := transientConsulError(test.err); got != test.retry {
			t.Fatalf("retry %v = %v", test.err, got)
		}
	}
}

func TestLoadFiltersSiblingPrefixesAndKeepsExactFile(t *testing.T) {
	for _, path := range []string{"settings", "settings/", "settings.yaml"} {
		t.Run(path, func(t *testing.T) {
			client, err := consulapi.NewClient(&consulapi.Config{HttpClient: &http.Client{Transport: consulRoundTripFunc(func(*http.Request) (*http.Response, error) {
				pairs := consulapi.KVPairs{{Key: "settings/value.yaml", Value: []byte("value: good")}, {Key: "settings-other/value.yaml", Value: []byte("value: wrong")}, {Key: "settings.yaml", Value: []byte("value: exact")}, {Key: "settings/", Value: nil}}
				data, err := json.Marshal(pairs)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"X-Consul-Index": []string{"1"}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			values, err := (&kvSource{client: client, path: path}).Load()
			if err != nil {
				t.Fatal(err)
			}
			if len(values) != 1 {
				t.Fatalf("values = %v", values)
			}
			expected := "value: good"
			if path == "settings.yaml" {
				expected = "value: exact"
			}
			if string(values[0].Value) != expected {
				t.Fatalf("value = %q, want %q", values[0].Value, expected)
			}
		})
	}
}

func TestExternalConsulSnapshotUpdateDeletionAndStop(t *testing.T) {
	address := os.Getenv("FOUNDATION_TEST_CONSUL_ADDR")
	if address == "" {
		t.Skip("set FOUNDATION_TEST_CONSUL_ADDR for Docker integration tests")
	}
	client, err := consulapi.NewClient(&consulapi.Config{Address: address})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	prefix := fmt.Sprintf("foundation-test-%d", time.Now().UnixNano())
	put := func(value string) {
		t.Helper()
		if _, err := client.KV().Put(&consulapi.KVPair{Key: prefix + "/value.json", Value: []byte(value)}, new(consulapi.WriteOptions).WithContext(ctx)); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := client.KV().DeleteTree(prefix, new(consulapi.WriteOptions).WithContext(ctx)); err != nil {
			t.Error(err)
		}
	})
	put(`{"feature":{"value":1}}`)
	logger, release, err := log.NewLogger()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	sources, err := NewSources(client, logger, PathList{prefix})
	if err != nil {
		t.Fatal(err)
	}
	base, err := text.NewSource("base.json", "json", `{"feature":{"value":0}}`)
	if err != nil {
		t.Fatal(err)
	}
	manager, cleanup, err := foundationconfig.NewManager(append(foundationconfig.Sources{base}, sources...))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	type feature struct {
		Value int `json:"value"`
	}
	hot, stop, err := foundationconfig.NewHotReloadValue[feature](manager, "feature")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	wait := func(want int) {
		t.Helper()
		timer := time.NewTicker(5 * time.Millisecond)
		defer timer.Stop()
		for {
			current, _ := hot.GetCurrent()
			if current.Value == want {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatalf("config value=%d want=%d", current.Value, want)
			case <-timer.C:
			}
		}
	}
	wait(1)
	start := time.Now()
	put(`{"feature":{"value":2}}`)
	wait(2)
	t.Logf("update visible=%s", time.Since(start))
	if _, err := client.KV().DeleteTree(prefix, new(consulapi.WriteOptions).WithContext(ctx)); err != nil {
		t.Fatal(err)
	}
	wait(0)
	stop()
	start = time.Now()
	cleanup()
	t.Logf("deleted override restored base; cleanup=%s", time.Since(start))
}
