package decoder

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

func TestDecoderKeepsDefaultsAndInputsIndependentAcrossApplications(t *testing.T) {
	type settings struct {
		Labels map[string]string `json:"labels"`
		Values []int             `json:"values"`
	}
	defaults := &settings{Labels: map[string]string{"region": "base", "env": "test"}, Values: []int{1, 2}}
	valueDecoder, err := New(new(settings), []any{defaults})
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"labels": map[string]any{"region": "override"}, "values": []any{3}}
	for range 2 {
		target := new(settings)
		if err := valueDecoder.Apply(input, true, target); err != nil {
			t.Fatal(err)
		}
		want := &settings{Labels: map[string]string{"region": "override", "env": "test"}, Values: []int{3}}
		if !reflect.DeepEqual(target, want) {
			t.Fatalf("Apply = %+v, want %+v", target, want)
		}
		target.Labels["region"] = "mutated"
		target.Values[0] = 9
		if err := valueDecoder.Apply(nil, false, target); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(target, defaults) {
			t.Fatalf("default fallback = %+v, want %+v", target, defaults)
		}
	}
	if input["labels"].(map[string]any)["env"] != nil || input["values"].([]any)[0] != 3 {
		t.Fatalf("Apply mutated input: %#v", input)
	}
}

func BenchmarkDecoderDefaults(b *testing.B) {
	defaults := map[string]map[string]int{}
	for i := range 100 {
		defaults[fmt.Sprint(i)] = map[string]int{"default": i, "retained": i}
	}
	input := map[string]any{"0": map[string]any{"default": 200}}
	valueDecoder, err := New(new(map[string]map[string]int), []any{&defaults})
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		target := new(map[string]map[string]int)
		if err := valueDecoder.Apply(input, true, target); err != nil {
			b.Fatal(err)
		}
	}
}

func TestDecoderSupportsGoAndProtobufTargets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		value  any
		target any
		check  func(testing.TB, any)
	}{
		{
			name:   "number",
			value:  float64(7),
			target: new(int),
			check: func(t testing.TB, target any) {
				if got := *target.(*int); got != 7 {
					t.Fatalf("number = %d, want 7", got)
				}
			},
		},
		{
			name:   "map",
			value:  map[string]any{"env": "test"},
			target: new(map[string]string),
			check: func(t testing.TB, target any) {
				if got := (*target.(*map[string]string))["env"]; got != "test" {
					t.Fatalf("env = %q, want test", got)
				}
			},
		},
		{
			name:   "slice",
			value:  []any{float64(1), float64(2)},
			target: new([]int),
			check: func(t testing.TB, target any) {
				got := *target.(*[]int)
				if len(got) != 2 || got[0] != 1 || got[1] != 2 {
					t.Fatalf("slice = %v, want [1 2]", got)
				}
			},
		},
		{
			name: "protobuf",
			value: map[string]any{
				"tags":                 []any{"blue"},
				"healthcheck_internal": "2s",
			},
			target: new(config_pb.Registry),
			check: func(t testing.TB, target any) {
				got := target.(*config_pb.Registry)
				if len(got.GetTags()) != 1 || got.GetTags()[0] != "blue" ||
					got.GetHealthcheckInternal().AsDuration() != 2*time.Second {
					t.Fatalf("protobuf = %v", got)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			valueDecoder, err := New(tt.target, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := valueDecoder.Apply(tt.value, true, tt.target); err != nil {
				t.Fatal(err)
			}
			tt.check(t, tt.target)
		})
	}
}

func TestDecoderClearsReusedTargetAndCreatesFreshTargets(t *testing.T) {
	t.Parallel()

	type feature struct {
		Enabled bool   `json:"enabled"`
		Stale   string `json:"stale"`
	}
	target := &feature{Stale: "old"}
	valueDecoder, err := New(target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := valueDecoder.Apply(map[string]any{"enabled": true}, true, target); err != nil {
		t.Fatal(err)
	}
	if !target.Enabled || target.Stale != "" {
		t.Fatalf("target = %+v", target)
	}
	first := valueDecoder.NewTarget()
	second := valueDecoder.NewTarget()
	if first == second || first == target || second == target {
		t.Fatal("Decoder reused target allocation")
	}
}

func TestDecoderValidatesTargetsAndDefaults(t *testing.T) {
	t.Parallel()

	var nilTarget *config_pb.Server
	tests := []struct {
		name     string
		target   any
		defaults []any
		needle   string
	}{
		{name: "nil", needle: "target is nil"},
		{name: "value", target: config_pb.Server{}, needle: "must be a pointer"},
		{name: "nil pointer", target: nilTarget, needle: "nil pointer"},
		{name: "different default", target: new(config_pb.Server), defaults: []any{new(config_pb.Client)}, needle: "does not match"},
		{name: "too many defaults", target: new(config_pb.Server), defaults: []any{new(config_pb.Server), new(config_pb.Server)}, needle: "at most one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(tt.target, tt.defaults)
			if err == nil || !strings.Contains(err.Error(), tt.needle) {
				t.Fatalf("New error = %v, want substring %q", err, tt.needle)
			}
		})
	}

	target := new(struct{})
	valueDecoder, err := New(target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := valueDecoder.Apply(nil, false, target); !errors.Is(err, kratosconfig.ErrNotFound) {
		t.Fatalf("Apply missing error = %v, want ErrNotFound", err)
	}
}

func TestDecoderPreservesIntegerDefaultsAndOverrides(t *testing.T) {
	type limits struct {
		Signed   int64   `json:"signed"`
		Unsigned uint64  `json:"unsigned"`
		Nested   []int64 `json:"nested"`
	}
	defaults := &limits{Signed: math.MaxInt64, Unsigned: math.MaxUint64, Nested: []int64{math.MinInt64, 9007199254740993}}
	target := new(limits)
	decoder, err := New(target, []any{defaults})
	if err != nil {
		t.Fatal(err)
	}
	if err := decoder.Apply(map[string]any{"signed": json.Number("9007199254740993")}, true, target); err != nil {
		t.Fatal(err)
	}
	if target.Signed != 9007199254740993 || target.Unsigned != math.MaxUint64 || len(target.Nested) != 2 || target.Nested[0] != math.MinInt64 || target.Nested[1] != 9007199254740993 {
		t.Fatalf("Apply = %+v", target)
	}
	target.Nested[0] = 0
	if defaults.Nested[0] != math.MinInt64 || defaults.Signed != math.MaxInt64 {
		t.Fatal("Apply mutated defaults")
	}
}
