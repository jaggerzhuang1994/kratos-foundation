package config_test

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestManagerLoadsCommonGoAndProtobufTargets(t *testing.T) {
	source := newJSONSource(`{
		"number": 7,
		"labels": {"env": "test"},
		"items": [1, 2],
		"registry": {"tags": ["blue"], "healthcheck_internal": "3s"}
	}`)
	manager, cleanup, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	var number int
	if err := manager.Load("number", &number); err != nil {
		t.Fatal(err)
	}
	if number != 7 {
		t.Fatalf("number = %d, want 7", number)
	}

	labels := map[string]string{"stale": "value"}
	if err := manager.Load("labels", &labels); err != nil {
		t.Fatal(err)
	}
	if len(labels) != 1 || labels["env"] != "test" {
		t.Fatalf("labels = %#v, want env=test only", labels)
	}

	var items []int
	if err := manager.Load("items", &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0] != 1 || items[1] != 2 {
		t.Fatalf("items = %v, want [1 2]", items)
	}

	registry := new(config_pb.Registry)
	if err := manager.Load("registry", registry); err != nil {
		t.Fatal(err)
	}
	if got := registry.GetTags(); len(got) != 1 || got[0] != "blue" {
		t.Fatalf("registry tags = %v, want [blue]", got)
	}
	if got := registry.GetHealthcheckInternal().AsDuration(); got != 3*time.Second {
		t.Fatalf("registry healthcheck interval = %s, want 3s", got)
	}
}

func TestManagerMergesDefaultsWithoutMutatingThem(t *testing.T) {
	source := newJSONSource(`{
		"registry": {
			"disableHeartbeat": false,
			"healthcheck-internal": "0s",
			"tags": []
		}
	}`)
	manager, cleanup, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	defaults := &config_pb.Registry{
		DisableHeartbeat:    proto.Bool(true),
		HealthcheckInternal: durationpb.New(10 * time.Second),
		Tags:                []string{"default"},
	}
	target := new(config_pb.Registry)
	if err := manager.Load("registry", target, defaults); err != nil {
		t.Fatal(err)
	}
	if target.GetDisableHeartbeat() {
		t.Fatal("explicit false did not override default true")
	}
	if got := target.GetHealthcheckInternal().AsDuration(); got != 0 {
		t.Fatalf("explicit duration = %s, want 0s", got)
	}
	if len(target.GetTags()) != 0 {
		t.Fatalf("explicit tags = %v, want empty", target.GetTags())
	}
	if !defaults.GetDisableHeartbeat() ||
		defaults.GetHealthcheckInternal().AsDuration() != 10*time.Second ||
		len(defaults.GetTags()) != 1 {
		t.Fatal("Load mutated caller-owned defaults")
	}
}

func TestManagerRejectsInvalidTargetsAndObservers(t *testing.T) {
	manager, cleanup, err := config.NewManager(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	var nilTarget *config_pb.Server
	tests := []struct {
		name    string
		target  any
		wantErr string
	}{
		{name: "nil", wantErr: "target is nil"},
		{name: "value", target: config_pb.Server{}, wantErr: "must be a pointer"},
		{name: "nil pointer", target: nilTarget, wantErr: "nil pointer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.Load("server", tt.target)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
	if _, err := manager.Subscribe("server", new(config_pb.Server), nil); err == nil {
		t.Fatal("Subscribe accepted nil observer")
	}
	if err := manager.Load("server", new(config_pb.Server), new(config_pb.Client)); err == nil ||
		!strings.Contains(err.Error(), "does not match") {
		t.Fatalf("Load mismatched default error = %v", err)
	}
	if err := manager.Load(
		"server",
		new(config_pb.Server),
		new(config_pb.Server),
		new(config_pb.Server),
	); err == nil || !strings.Contains(err.Error(), "at most one") {
		t.Fatalf("Load multiple defaults error = %v", err)
	}
	if _, err := manager.Subscribe("server", config_pb.Server{}, func(string, any, error) {}); err == nil ||
		!strings.Contains(err.Error(), "must be a pointer") {
		t.Fatalf("Subscribe non-pointer prototype error = %v", err)
	}
}

func TestManagerSupportsEmptySourcesAndMissingDefaults(t *testing.T) {
	manager, cleanup, err := config.NewManager(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	type feature struct {
		Enabled bool `json:"enabled"`
	}
	target := new(feature)
	if err := manager.Load("feature", target, &feature{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if !target.Enabled {
		t.Fatal("missing key did not use explicit default")
	}
	if err := manager.Load("missing", new(feature)); !errors.Is(err, config.ErrNotFound) {
		t.Fatalf("Load missing error = %v, want ErrNotFound", err)
	}
	if _, err := manager.Subscribe("missing", new(feature), func(string, any, error) {}); !errors.Is(err, config.ErrNotFound) {
		t.Fatalf("Subscribe missing error = %v, want ErrNotFound", err)
	}
}

func TestManagerLoadsYAMLFormatThroughPublicSourceContract(t *testing.T) {
	source := &testSource{
		values: []*config.KeyValue{{
			Key:    "config.yaml",
			Format: config.YAMLFormat,
			Value:  []byte("feature:\n  enabled: true\n"),
		}},
		watcher: newTestWatcher(),
	}
	manager, cleanup, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	var feature struct {
		Enabled bool `json:"enabled"`
	}
	if err := manager.Load("feature", &feature); err != nil {
		t.Fatal(err)
	}
	if !feature.Enabled {
		t.Fatal("YAML source was not decoded through public format contract")
	}
}

type integerConfig struct {
	Signed   int64   `json:"signed"`
	Unsigned uint64  `json:"unsigned"`
	Nested   []int64 `json:"nested"`
}

func TestManagerPreservesIntegerPrecisionFromSourcesAndDefaults(t *testing.T) {
	want := &integerConfig{Signed: math.MaxInt64, Unsigned: math.MaxUint64, Nested: []int64{math.MinInt64, 9007199254740993}}
	for _, test := range []struct {
		name     string
		content  string
		defaults []any
	}{
		{name: "JSON source", content: `{"limits":{"signed":9223372036854775807,"unsigned":18446744073709551615,"nested":[-9223372036854775808,9007199254740993]}}`},
		{name: "defaults", content: `{}`, defaults: []any{want}},
		{name: "merged defaults", content: `{"limits":{"signed":9223372036854775807}}`, defaults: []any{want}},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, cleanup, err := config.NewManager(config.Sources{newJSONSource(test.content)})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			got := new(integerConfig)
			if err := manager.Load("limits", got, test.defaults...); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Load = %+v, want %+v", got, want)
			}
		})
	}
}

func TestManagerRejectsRemovedFoundationFields(t *testing.T) {
	for _, test := range []struct{ name, body, path string }{
		{"root", `{"log":{"level":"debug"}}`, "log"},
		{"nested", `{"app":{"disableRegistrar":true}}`, "app.disableRegistrar"},
		{"timeout", `{"server":{"middleware":{"timeout":{"default":"1s"}}}}`, "server.middleware.timeout"},
		{"map", `{"database":{"connections":{"main":{"replicas":[]}}}}`, "database.connections.main.replicas"},
		{"client", `{"client":{"clients":{"orders":{"middleware":{"timeout":{}}}}}}`, "client.clients.orders.middleware.timeout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, cleanup, err := config.NewManager(config.NewSources(newJSONSource(test.body)))
			if cleanup != nil {
				cleanup()
			}
			if err == nil || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want removed path %s", err, test.path)
			}
		})
	}
}
