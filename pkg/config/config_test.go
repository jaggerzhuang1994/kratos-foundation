package config_test

import (
	"os"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestManagerLoadsEnvironmentNumbers(t *testing.T) {
	t.Setenv("CONFIG_ENV_NUMBER", "9223372036854775807")
	manager, cleanup, err := config.NewManager(config.Sources{newJSONSource(`{"number":${CONFIG_ENV_NUMBER}}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	var plain int64
	if err := manager.Load("number", &plain); err != nil {
		t.Fatal(err)
	}
	protobuf := new(wrapperspb.Int64Value)
	if err := manager.Load("number", protobuf); err != nil {
		t.Fatal(err)
	}
	if plain != 9223372036854775807 || protobuf.Value != plain {
		t.Fatalf("numbers = %d, %d", plain, protobuf.Value)
	}
}

func TestManagerEnvironmentUpdateRecovery(t *testing.T) {
	t.Setenv("CONFIG_ENV_UPDATE", "1")
	source := newJSONSource(`{"number":${CONFIG_ENV_UPDATE:?number required}}`)
	manager, cleanup, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	events := make(chan observedValue, 4)
	cancel, err := manager.Subscribe("number", new(int), func(_ string, value any, err error) {
		events <- observedValue{value: value, err: err}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	if event := receiveObservedValue(t, events); event.err != nil || *event.value.(*int) != 1 {
		t.Fatalf("initial = %+v", event)
	}
	if err := os.Unsetenv("CONFIG_ENV_UPDATE"); err != nil {
		t.Fatal(err)
	}
	source.publish(jsonValues(`{"number":${CONFIG_ENV_UPDATE:?number required}}`), nil)
	if event := receiveObservedValue(t, events); event.err == nil {
		t.Fatal("missing environment update accepted")
	}
	var current int
	if err := manager.Load("number", &current); err != nil || current != 1 {
		t.Fatalf("retained = %d, %v", current, err)
	}
	t.Setenv("CONFIG_ENV_UPDATE", "2")
	source.publish(jsonValues(`{"number":${CONFIG_ENV_UPDATE:?number required}}`), nil)
	if event := receiveObservedValue(t, events); event.err != nil || *event.value.(*int) != 2 {
		t.Fatalf("recovered = %+v", event)
	}
}

func TestManagerRejectsRequiredEnvironmentBeforeParsing(t *testing.T) {
	t.Setenv("CONFIG_ENV_REQUIRED", "")
	for _, key := range []bool{false, true} {
		source := newJSONSource(`{"value":"${CONFIG_ENV_REQUIRED:?value required}"}`)
		if key {
			source.values[0].Key = "${CONFIG_ENV_REQUIRED:?key required}"
		}
		manager, cleanup, err := config.NewManager(config.Sources{source})
		if err == nil {
			cleanup()
			t.Fatal("required environment accepted")
		}
		if manager != nil || source.watcher.stopCalls.Load() != 1 {
			t.Fatal("failed construction did not release source")
		}
	}
}
