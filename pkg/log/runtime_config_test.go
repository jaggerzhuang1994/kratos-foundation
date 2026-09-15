package log

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/request"
	"google.golang.org/protobuf/proto"
)

func levelPointer(v kratoslog.Level) *kratoslog.Level { return &v }

// WithFilterKeys 仅供本包旧字段过滤用例设置完整策略。
func (s *sharedState) WithFilterKeys(keys ...string) {
	if err := s.applyRuntimeConfig(&RuntimeConfig{FilterKeys: keys}); err != nil {
		panic(err)
	}
}

func TestRuntimePolicyPrecedenceAndIsolation(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		instance              *kratoslog.Level
		debug, disabled, want bool
	}{
		{name: "env filters"}, {name: "instance debug", instance: levelPointer(kratoslog.LevelDebug), want: true},
		{name: "request modules instance", instance: levelPointer(kratoslog.LevelError), debug: true, want: true},
		{name: "request respects env disable", debug: true, disabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &sharedState{}
			state.custom.Store(&customState{})
			records := 0
			base := &logger{shared: state, config: &configState{level: kratoslog.LevelInfo, disabled: tc.disabled, msgKey: "message", output: loggerFunc(func(kratoslog.Level, ...any) error { records++; return nil })}}
			var view Logger = base
			if tc.instance != nil {
				view = view.WithLevel(*tc.instance)
			}
			if tc.debug {
				view = view.WithContext(request.WithDebug(context.Background()))
			}
			if err := state.applyRuntimeConfig(&RuntimeConfig{FilterKeys: []string{"secret"}}); err != nil {
				t.Fatal(err)
			}
			view.Debug("probe")
			if (records == 1) != tc.want {
				t.Fatalf("records=%d want=%v", records, tc.want)
			}
			records = 0
			base.Debug("other")
			if records != 0 {
				t.Fatal("derived policy leaked")
			}
		})
	}
}

func TestRuntimePolicyCopiesAndRejectsInvalidUpdates(t *testing.T) {
	state := &sharedState{}
	state.WithKV("service", "orders")
	size := 10
	policy := &RuntimeConfig{FilterKeys: []string{"secret"}, Std: &OutputPolicy{Disable: proto.Bool(false), Level: proto.String("warn"), FilterKeys: []string{}}, File: &FilePolicy{Enable: proto.Bool(false), Path: proto.String("app.log"), Rotating: &RotatingPolicy{MaxSize: &size}}}
	if err := state.applyRuntimeConfig(policy); err != nil {
		t.Fatal(err)
	}
	previous := state.custom.Load()
	policy.FilterKeys[0] = "changed"
	*policy.Std.Level = "fatal"
	size = 20
	if previous.filterKeys[0] != "secret" || *previous.policy.Std.Level != "warn" || *previous.policy.File.Rotating.MaxSize != 10 || previous.policy.Std.FilterKeys == nil {
		t.Fatal("snapshot alias or empty-list presence lost")
	}
	negative := -1
	for _, invalid := range []*RuntimeConfig{nil, {FilterKeys: []string{"secret", "secret"}}, {Std: &OutputPolicy{Level: proto.String("bad")}}, {File: &FilePolicy{Path: proto.String("")}}, {File: &FilePolicy{Level: proto.String("bad")}}, {File: &FilePolicy{Rotating: &RotatingPolicy{MaxSize: &negative}}}} {
		if err := state.applyRuntimeConfig(invalid); err == nil {
			t.Fatalf("accepted invalid %v", invalid)
		}
		if state.custom.Load() != previous {
			t.Fatal("invalid update changed snapshot")
		}
	}
	if err := state.applyRuntimeConfig(&RuntimeConfig{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.custom.Load().kv, []any{"service", "orders"}) {
		t.Fatal("lost metadata")
	}
}

func TestRuntimeFilterPresenceAndInstanceBoundary(t *testing.T) {
	state := &sharedState{}
	state.custom.Store(&customState{})
	var fields []any
	base := &logger{shared: state, config: &configState{level: kratoslog.LevelInfo, msgKey: "msg", filterKeys: []string{"env.secret"}, output: loggerFunc(func(_ kratoslog.Level, kv ...any) error { fields = kv; return nil })}}
	view := base.WithFilterKeys("instance.secret").WithContext(request.WithDebug(context.Background()))
	for _, tc := range []struct {
		raw        string
		envVisible bool
	}{{`{}`, false}, {`{"filter_keys":[]}`, true}, {`{"filter_keys":["env.secret"]}`, false}, {`{}`, false}} {
		var policy RuntimeConfig
		if err := json.Unmarshal([]byte(tc.raw), &policy); err != nil {
			t.Fatal(err)
		}
		if err := state.applyRuntimeConfig(&policy); err != nil {
			t.Fatal(err)
		}
		view.Debugw("env.secret", "env-value", "instance.secret", "instance-value")
		if containsLogValue(fields, "env-value") != tc.envVisible || containsLogValue(fields, "instance-value") {
			t.Fatalf("policy=%s fields=%v", tc.raw, fields)
		}
	}
}

func TestPublicRuntimeConfigPublisher(t *testing.T) {
	previous := processState.custom.Load()
	t.Cleanup(func() { processState.custom.Store(previous) })
	if err := ApplyRuntimeConfig(&RuntimeConfig{}); err != nil {
		t.Fatal(err)
	}
}

func TestModulePolicyFirstMatchAndPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		module   string
		rules    []ModulePolicy
		instance kratoslog.Level
		debug    bool
		want     bool
	}{
		{name: "exact modules static error", module: "orders", instance: kratoslog.LevelError, rules: []ModulePolicy{{Module: "orders", Level: proto.String("debug")}}, want: true},
		{name: "config raises static debug", instance: kratoslog.LevelDebug, module: "orders", rules: []ModulePolicy{{Module: "orders", Level: proto.String("error")}}},
		{name: "prefix first hides exact", module: "orders.api", rules: []ModulePolicy{{Module: "orders*", Level: proto.String("error")}, {Module: "orders.api", Level: proto.String("debug")}}},
		{name: "exact first hides prefix", module: "orders.api", instance: kratoslog.LevelError, rules: []ModulePolicy{{Module: "orders.api", Level: proto.String("debug")}, {Module: "orders*", Level: proto.String("error")}}, want: true},
		{name: "no match inherits", instance: kratoslog.LevelDebug, module: "orders.api", rules: []ModulePolicy{{Module: "orders", Disable: proto.Bool(true)}}, want: true},
		{name: "empty first does not fall through", instance: kratoslog.LevelDebug, module: "orders", rules: []ModulePolicy{{Module: "orders"}, {Module: "*", Disable: proto.Bool(true)}}, want: true},
		{name: "star catches unknown", rules: []ModulePolicy{{Module: "*", Disable: proto.Bool(true)}}},
		{name: "default module unknown", rules: []ModulePolicy{{Module: "unknown", Disable: proto.Bool(true)}}},
		{name: "false shadows disabled fallback", instance: kratoslog.LevelDebug, module: "orders", rules: []ModulePolicy{{Module: "orders", Disable: proto.Bool(false)}, {Module: "*", Disable: proto.Bool(true)}}, want: true},
		{name: "request debug modules module level", module: "orders", instance: kratoslog.LevelError, debug: true, rules: []ModulePolicy{{Module: "orders", Level: proto.String("error")}}, want: true},
		{name: "request debug respects module disable", module: "orders", debug: true, rules: []ModulePolicy{{Module: "orders", Disable: proto.Bool(true)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &sharedState{}
			state.custom.Store(&customState{})
			written := 0
			root := &logger{shared: state, config: &configState{level: kratoslog.LevelInfo, msgKey: "msg", output: loggerFunc(func(kratoslog.Level, ...any) error { written++; return nil })}}
			view := root.WithLevel(tc.instance)
			if tc.module != "" {
				view = view.WithModule(tc.module)
			}
			if tc.debug {
				view = view.WithContext(request.WithDebug(context.Background()))
			}
			if err := state.applyRuntimeConfig(&RuntimeConfig{Modules: tc.rules}); err != nil {
				t.Fatal(err)
			}
			view.Debug("event")
			if (written == 1) != tc.want {
				t.Fatalf("written=%d want=%v", written, tc.want)
			}
		})
	}
}

func TestModulePolicyHotUpdateAccumulatesFilters(t *testing.T) {
	state := &sharedState{}
	state.custom.Store(&customState{})
	var fields []any
	root := &logger{shared: state, config: &configState{level: kratoslog.LevelInfo, msgKey: "msg", output: loggerFunc(func(_ kratoslog.Level, kv ...any) error { fields = kv; return nil })}}
	view := root.WithModule("orders").WithLevel(kratoslog.LevelError).WithFilterKeys("local")
	rule := ModulePolicy{Module: "orders", Level: proto.String("debug"), FilterKeys: []string{"module", "module_secret", "secret.*"}}
	policy := &RuntimeConfig{FilterKeys: []string{"root_secret"}, Modules: []ModulePolicy{rule, {Module: "*", FilterKeys: []string{"later"}}}}
	if err := state.applyRuntimeConfig(policy); err != nil {
		t.Fatal(err)
	}
	// 发布后修改调用方输入不得污染快照。
	policy.Modules[0].Module = "changed"
	*policy.Modules[0].Level = "fatal"
	policy.Modules[0].FilterKeys[1] = "changed"
	view.Debugw("root_secret", "root-value", "module_secret", "module-value", "secret.token", "prefix-value", "local", "local-value", "later", "later-value", "visible", "kept")
	for _, value := range []string{"root-value", "module-value", "prefix-value", "local-value"} {
		if containsLogValue(fields, value) {
			t.Fatalf("unfiltered %q: %v", value, fields)
		}
	}
	if !containsLogValue(fields, "later-value") || !containsLogValue(fields, "orders") || !containsLogValue(fields, "kept") {
		t.Fatalf("first match or module identity lost: %v", fields)
	}
	if err := state.applyRuntimeConfig(&RuntimeConfig{Modules: []ModulePolicy{}}); err != nil {
		t.Fatal(err)
	}
	fields = nil
	view.Debug("filtered by original WithLevel")
	if fields != nil {
		t.Fatal("removed override retained old level")
	}
	view.Errorw("module_secret", "visible-again", "local", "local-value")
	if !containsLogValue(fields, "visible-again") || containsLogValue(fields, "local-value") {
		t.Fatalf("stale filter cache: %v", fields)
	}
}

func TestModulePolicyRejectsInvalidPolicy(t *testing.T) {
	state := &sharedState{}
	state.custom.Store(&customState{})
	previous := state.custom.Load()
	for _, rule := range []ModulePolicy{
		{}, {Module: " orders"}, {Module: "a*b"}, {Module: "a**"}, {Module: "a?"}, {Module: "[a]"},
		{Module: "orders", Level: proto.String("verbose")}, {Module: "orders", FilterKeys: []string{""}},
		{Module: "orders", FilterKeys: []string{" key"}}, {Module: "orders", FilterKeys: []string{"key", "key"}},
	} {
		if err := state.applyRuntimeConfig(&RuntimeConfig{Modules: []ModulePolicy{rule}}); err == nil {
			t.Fatalf("invalid accepted: %#v", rule)
		}
		if state.custom.Load() != previous {
			t.Fatal("invalid policy replaced snapshot")
		}
	}
	if err := state.applyRuntimeConfig(&RuntimeConfig{Modules: []ModulePolicy{{Module: "*"}, {Module: "*"}}}); err == nil {
		t.Fatal("duplicate expression accepted")
	}
}
