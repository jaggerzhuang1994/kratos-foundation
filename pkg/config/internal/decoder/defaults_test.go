package decoder

import (
	"reflect"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestApplyDefaultsAppliesNormalizedDefaultsAndReplacesSlices(t *testing.T) {
	t.Parallel()

	defaults := &config_pb.RegistrarOptions{
		DisableHeartbeat:    proto.Bool(true),
		HealthcheckInternal: durationpb.New(10 * time.Second),
		Tags:                []string{"default"},
	}
	target := new(config_pb.RegistrarOptions)
	valueDecoder, err := New(target, []any{defaults})
	if err != nil {
		t.Fatal(err)
	}
	if err := valueDecoder.Apply(map[string]any{
		"disableHeartbeat":     false,
		"healthcheck-internal": "0s",
		"tags":                 []any{},
	}, true, target); err != nil {
		t.Fatal(err)
	}
	if target.GetDisableHeartbeat() || target.GetHealthcheckInternal().AsDuration() != 0 || len(target.GetTags()) != 0 {
		t.Fatalf("target = %v", target)
	}
	if !defaults.GetDisableHeartbeat() || defaults.GetHealthcheckInternal().AsDuration() != 10*time.Second || len(defaults.GetTags()) != 1 {
		t.Fatal("Decoder mutated caller-owned defaults")
	}
}

func TestApplyDefaultsPreservesBusinessMapKeys(t *testing.T) {
	defaults := map[string]int{"foo_bar": 1, "FooBar": 3}
	target := new(map[string]int)
	decoder, err := New(target, []any{&defaults})
	if err != nil {
		t.Fatal(err)
	}
	if err := decoder.Apply(map[string]any{"foobar": 2}, true, target); err != nil {
		t.Fatal(err)
	}
	if want := (map[string]int{"foo_bar": 1, "FooBar": 3, "foobar": 2}); !reflect.DeepEqual(*target, want) {
		t.Fatalf("got %v, want %v", *target, want)
	}
}

func TestApplyDefaultsPreservesProtoMapKeysAndNestedFieldAliases(t *testing.T) {
	defaults := &config_pb.Client{Clients: map[string]*config_pb.ClientOption{"order_service": {Target: "old"}}}
	target := new(config_pb.Client)
	decoder, err := New(target, []any{defaults})
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"clients": map[string]any{"order-service": map[string]any{"target": "new"}, "order_service": map[string]any{"TARGET": "updated"}}}
	if err := decoder.Apply(input, true, target); err != nil {
		t.Fatal(err)
	}
	if len(target.Clients) != 2 || target.Clients["order_service"].GetTarget() != "updated" || target.Clients["order-service"].GetTarget() != "new" {
		t.Fatalf("clients = %v", target.Clients)
	}
}
