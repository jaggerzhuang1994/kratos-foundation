package consul

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	consulapi "github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/text"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

func TestNewSourcesReturnsOneSourcePerPath(t *testing.T) {
	client, err := consulapi.NewClient(consulapi.DefaultConfig())
	if err != nil {
		t.Fatalf("create Consul client: %v", err)
	}
	paths := PathList{"service/base", "service/override"}

	sources, err := newSources(client, paths)
	if err != nil {
		t.Fatalf("newSources() error = %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("newSources() count = %d, want 2", len(sources))
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

	empty, err := newSources(client, nil)
	if err != nil || empty != nil {
		t.Fatalf("newSources(empty) = %#v, %v; want nil, nil", empty, err)
	}
	disabled, err := newSources(nil, PathList{"service/config"})
	if err != nil || disabled != nil {
		t.Fatalf("newSources(nil client) = %#v, %v; want nil, nil", disabled, err)
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
			if _, err := newSources(client, test.paths); err == nil {
				t.Fatal("newSources() error = nil")
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

func TestLoadResolvesFilesDirectoriesAndPatterns(t *testing.T) {
	for _, tc := range []struct{ name, pattern, want string }{
		{"literal key", "settings", "settings"},
		{"literal file", "settings.yaml", "settings.yaml"},
		{"literal file under directory", "settings/value.yaml", "value.yaml"},
		{"directory marker", "settings/", "value.yaml"},
		{"implicit directory", "configs/app", "value.yaml"},
		{"escaped directory", `configs/\[app\]`, "value.yaml"},
		{"missing key", "missing", ""},
		{"explicit wildcard", "settings/*.yaml", "value.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := consulapi.NewClient(&consulapi.Config{HttpClient: &http.Client{Transport: consulRoundTripFunc(func(*http.Request) (*http.Response, error) {
				pairs := consulapi.KVPairs{
					{Key: "settings", Value: []byte("literal")},
					{Key: "settings/value.yaml", Value: []byte("value: good")},
					{Key: "settings/prod/value.yaml", Value: []byte("value: nested")},
					{Key: "settings-other/value.yaml", Value: []byte("value: wrong")},
					{Key: "settings.yaml", Value: []byte("value: exact")},
					{Key: "settings.yaml/child.yaml", Value: []byte("value: child")},
					{Key: "settings/"},
					{Key: "configs/app/value.yaml"},
					{Key: "configs/app/value.yml"},
					{Key: "configs/app/nested/value.yaml"},
					{Key: "configs/app-other/value.yaml"},
					{Key: "configs/[app]/value.yaml"},
				}
				data, err := json.Marshal(pairs)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"X-Consul-Index": []string{"1"}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			values, err := (&kvSource{client: client, path: tc.pattern}).Load()
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(values) != 0 {
					t.Fatalf("unexpected values=%v", values)
				}
				return
			}
			if len(values) != 1 || values[0].Key != tc.want {
				t.Fatalf("values=%v want key=%s", values, tc.want)
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
	sources, err := newSources(client, PathList{prefix + "/*.json"})
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

func TestYAMLPatternFiltersAndSortsEverySnapshot(t *testing.T) {
	var output bytes.Buffer
	previous := log.GetLogger()
	log.SetLogger(kratoslog.NewStdLogger(&output))
	t.Cleanup(func() { log.SetLogger(previous) })
	for _, invalid := range []string{"configs/[.yaml", "configs/[].yaml", `configs/abc\`, "configs/[a-].yaml"} {
		if err := validatePaths(PathList{invalid}); !errors.Is(err, path.ErrBadPattern) {
			t.Fatalf("accepted %q", invalid)
		}
	}
	snapshots := []consulapi.KVPairs{
		{{Key: "configs/app/z.yaml", Value: []byte("value: last")}, {Key: "configs/app/prod/b.yaml"}, {Key: "configs/app/a.yaml", Value: []byte("value: first")}, {Key: "configs/app/x.yml"}, {Key: "configs/app-other/a.yaml"}},
		{{Key: "configs/app/new.yaml", Value: []byte("value: new")}},
		{},
	}
	for _, pattern := range []string{"configs/app/*.yaml", "configs/app", "configs/app/"} {
		for _, pairs := range snapshots {
			client, err := consulapi.NewClient(&consulapi.Config{HttpClient: &http.Client{Transport: consulRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				prefix := strings.TrimSuffix(pattern, "*.yaml")
				if r.URL.Path != "/v1/kv/"+prefix {
					t.Errorf("query path=%s", r.URL.Path)
				}
				data, err := json.Marshal(pairs)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"X-Consul-Index": []string{"2"}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			output.Reset()
			values, _, err := (&kvSource{client: client, path: pattern}).query(context.Background(), 1, time.Second, loadPhase)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range values {
				if !strings.Contains(output.String(), "path=configs/app/"+value.Key) || !strings.Contains(output.String(), "INFO") || !strings.Contains(output.String(), "loaded configuration file") {
					t.Fatalf("missing loaded path in log: %s", output.String())
				}
			}
			for _, excluded := range []string{"configs/app/prod/b.yaml", "configs/app/x.yml", "configs/app-other/a.yaml", "value: last", "value: first", "value: new"} {
				if strings.Contains(output.String(), excluded) {
					t.Fatalf("unexpected %q in log: %s", excluded, output.String())
				}
			}
			if len(values) == 0 && output.Len() != 0 {
				t.Fatalf("empty snapshot logged loaded files: %s", output.String())
			}
			switch len(pairs) {
			case 5:
				if len(values) != 2 || values[0].Key != "a.yaml" || values[1].Key != "z.yaml" {
					t.Fatalf("values=%v", values)
				}
			case 1:
				if len(values) != 1 || values[0].Key != "new.yaml" {
					t.Fatalf("new file=%v", values)
				}
			case 0:
				if len(values) != 0 {
					t.Fatalf("deleted files=%v", values)
				}
			}
		}
	}
}

func TestInitialLoadLogsEachFileOnce(t *testing.T) {
	var output bytes.Buffer
	previous := log.GetLogger()
	log.SetLogger(kratoslog.NewStdLogger(&output))
	t.Cleanup(func() { log.SetLogger(previous) })
	client, err := consulapi.NewClient(&consulapi.Config{HttpClient: &http.Client{Transport: consulRoundTripFunc(func(*http.Request) (*http.Response, error) {
		data, err := json.Marshal(consulapi.KVPairs{{Key: "configs/common.yaml", Value: []byte("feature: true")}})
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Consul-Index": []string{"2"}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	source := &kvSource{client: client, path: "configs/common.yaml"}
	if _, err := source.Load(); err != nil {
		t.Fatal(err)
	}
	watcher, err := source.Watch()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watcher.Stop() })
	if _, err := watcher.Next(); err != nil {
		t.Fatal(err)
	}
	logs := output.String()
	if strings.Count(logs, "loaded configuration file") != 1 ||
		strings.Count(logs, "path=configs/common.yaml") != 1 {
		t.Fatalf("unexpected initial load and watch logs: %s", logs)
	}
}

func TestPathMatchPatterns(t *testing.T) {
	for _, tc := range []struct{ name, pattern, prefix, key string }{
		{"literal without wildcard", "path/config.yaml", "path/config.yaml", "path/config.yaml"},
		{"multiple directories", "path/a/*/path/b/*/*.yaml", "path/a/", "path/a/team/path/b/prod/config.yaml"},
		{"partial directory", "path/a/team*/config*.yaml", "path/a/team", "path/a/team1/config-main.yaml"},
		{"different format", "path/*/*.json", "path/", "path/team/config.json"},
		{"empty prefix", "*/config.yaml", "", "team/config.yaml"},
		{"empty star match", "path/config.yaml*", "path/config.yaml", "path/config.yaml"},
		{"question", "path/team?/config.yaml", "path/team", "path/team1/config.yaml"},
		{"range", "path/[a-z]/config.yaml", "path/", "path/m/config.yaml"},
		{"negated range", "path/[^0-9]/config.yaml", "path/", "path/m/config.yaml"},
		{"unicode", "path/[甲乙]/config.yaml", "path/", "path/甲/config.yaml"},
		{"escaped star", `path/team\*/config?.yaml`, "path/team*/config", "path/team*/config1.yaml"},
		{"escaped question", `path/team\?/config.yaml`, "path/team?/config.yaml", "path/team?/config.yaml"},
		{"escaped bracket", `path/\[team\]/config.yaml`, "path/[team]/config.yaml", "path/[team]/config.yaml"},
		{"escaped backslash", `path/team\\/config*.yaml`, `path/team\/config`, `path/team\/config.yaml`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validatePaths(PathList{tc.pattern}); err != nil {
				t.Fatal(err)
			}
			requests := 0
			client, err := consulapi.NewClient(&consulapi.Config{HttpClient: &http.Client{Transport: consulRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/v1/kv/"+tc.prefix {
					t.Errorf("query=%s want prefix=%s", r.URL.Path, tc.prefix)
				}
				pairs := consulapi.KVPairs{{Key: tc.key, Value: []byte("{}")}, {Key: tc.key + "/nested.yaml"}, {Key: strings.Replace(tc.key, "/config", "/extra/config", 1)}, {Key: tc.prefix + "/"}}
				requests++
				if requests == 2 {
					pairs = nil
				}
				data, err := json.Marshal(pairs)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"X-Consul-Index": []string{"2"}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			source := &kvSource{client: client, path: tc.pattern}
			values, err := source.Load()
			if err != nil || len(values) != 1 || values[0].Key != strings.TrimPrefix(tc.key, tc.prefix[:strings.LastIndexByte(tc.prefix, '/')+1]) {
				t.Fatalf("values=%v err=%v", values, err)
			}
			watcher, err := source.Watch()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := watcher.Stop(); err != nil {
					t.Error(err)
				}
			})
			values, err = watcher.Next()
			if err != nil || len(values) != 0 {
				t.Fatalf("deleted snapshot=%v err=%v", values, err)
			}
			values, err = watcher.Next()
			if err != nil || len(values) != 1 || values[0].Key != strings.TrimPrefix(tc.key, tc.prefix[:strings.LastIndexByte(tc.prefix, '/')+1]) {
				t.Fatalf("new snapshot=%v err=%v", values, err)
			}

		})
	}
}

func TestLiteralPathReevaluatesExactKeyForEachSnapshot(t *testing.T) {
	pairs := consulapi.KVPairs{}
	// 同步 Transport 让测试按快照推进，无需共享 HTTP handler 状态。
	client, err := consulapi.NewClient(&consulapi.Config{HttpClient: &http.Client{
		Transport: consulRoundTripFunc(func(*http.Request) (*http.Response, error) {
			data, err := json.Marshal(pairs)
			if err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"X-Consul-Index": []string{"3"}},
				Body:       io.NopCloser(strings.NewReader(string(data))),
			}, nil
		}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	input := &kvSource{client: client, path: "settings"}
	watcher, err := input.Watch()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := watcher.Stop(); err != nil {
			t.Error(err)
		}
	})
	for _, exact := range []bool{true, false, true} {
		pairs = consulapi.KVPairs{{Key: "settings/value.yaml", Value: []byte("value: child")}}
		want := "value.yaml"
		if exact {
			pairs = append(pairs, &consulapi.KVPair{Key: "settings", Value: []byte("exact")})
			want = "settings"
		}
		values, err := watcher.Next()
		if err != nil || len(values) != 1 || values[0].Key != want {
			t.Fatalf("exact=%v values=%v err=%v", exact, values, err)
		}
	}
}

func TestConsulOutageRecoversWithOfficialMerge(t *testing.T) {
	var outage, deleted atomic.Bool
	watching, changed, failed := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	var watchingOnce, resumedOnce sync.Once
	resumed := make(chan struct{})
	remote, _ := consulTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("index") {
		case "1":
			watchingOnce.Do(func() { close(watching) })
			select {
			case <-changed:
			case <-r.Context().Done():
				return
			}
		case "2":
			resumedOnce.Do(func() { close(resumed) })
			<-r.Context().Done()
			return
		}
		if outage.Load() {
			select {
			case failed <- struct{}{}:
			default:
			}
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		values := consulapi.KVPairs{{Key: "settings/feature.json", Value: []byte(`{"feature":{"enabled":true}}`), ModifyIndex: 1}}
		w.Header().Set("X-Consul-Index", "1")
		if deleted.Load() {
			values = consulapi.KVPairs{}
			w.Header().Set("X-Consul-Index", "2")
		}
		_ = json.NewEncoder(w).Encode(values)
	})
	base, err := text.NewSource("base.json", "json", `{"feature":{"enabled":false}}`)
	if err != nil {
		t.Fatal(err)
	}
	manager, cleanup, err := foundationconfig.NewManager(foundationconfig.Sources{base, remote})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	type feature struct {
		Enabled bool `json:"enabled"`
	}
	hot, stop, err := foundationconfig.NewHotReloadValue[feature](manager, "feature")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	select {
	case <-watching:
	case <-time.After(time.Second):
		t.Fatal("blocking query did not start")
	}
	outage.Store(true)
	deleted.Store(true)
	close(changed)
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("outage was not observed")
	}
	if value, _ := hot.GetCurrent(); !value.Enabled {
		t.Fatal("outage discarded the valid remote snapshot")
	}
	if err := manager.Load("feature", new(feature)); err != nil {
		t.Fatalf("manager became terminal during outage: %v", err)
	}
	outage.Store(false)
	select {
	case <-resumed:
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not recover")
	}
	// 官方默认合并不会因空来源结果删除此前的覆盖值。
	if value, _ := hot.GetCurrent(); !value.Enabled {
		t.Fatal("empty update unexpectedly deleted cached value")
	}
}
