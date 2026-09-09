package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
)

func TestManagerReportsSnapshotErrorsAndRecovers(t *testing.T) {
	source := newJSONSource(`{"feature":{"value":1}}`)
	manager, cleanup, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	events := make(chan observedValue, 3)
	cancel, err := manager.Subscribe(
		"feature",
		new(struct {
			Value int `json:"value"`
		}),
		func(_ string, value any, err error) {
			events <- observedValue{value: value, err: err}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	receiveObservedValue(t, events)

	malformed := jsonValues(`{"feature":`)
	source.publish(malformed, malformed)
	failed := receiveObservedValue(t, events)
	if failed.err == nil {
		t.Fatal("malformed update did not reach observer")
	}

	valid := jsonValues(`{"feature":{"value":2}}`)
	source.publish(valid, valid)
	recovered := receiveObservedValue(t, events)
	if recovered.err != nil {
		t.Fatalf("valid update after error = %v", recovered.err)
	}
	if got := recovered.value.(*struct {
		Value int `json:"value"`
	}).Value; got != 2 {
		t.Fatalf("recovered value = %d, want 2", got)
	}
}

func TestManagerReportsTerminalWatcherFailure(t *testing.T) {
	source := newJSONSource(`{"feature":{"value":1}}`)
	manager, cleanup, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	events := make(chan observedValue, 2)
	cancel, err := manager.Subscribe(
		"feature",
		new(struct {
			Value int `json:"value"`
		}),
		func(_ string, value any, err error) {
			events <- observedValue{value: value, err: err}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	receiveObservedValue(t, events)

	cause := errors.New("watch connection lost")
	source.failWatcher(cause)
	failed := receiveObservedValue(t, events)
	if !errors.Is(failed.err, config.ErrWatcherStopped) || !errors.Is(failed.err, cause) {
		t.Fatalf("observer error = %v, want watcher stopped and cause", failed.err)
	}
	if err := manager.Load("feature", new(struct{})); !errors.Is(err, config.ErrWatcherStopped) {
		t.Fatalf("Load after watcher failure = %v, want ErrWatcherStopped", err)
	}
	if _, err := manager.Subscribe("feature", new(struct{}), func(string, any, error) {}); !errors.Is(err, config.ErrWatcherStopped) {
		t.Fatalf("Subscribe after watcher failure = %v, want ErrWatcherStopped", err)
	}
}

func TestNewManagerRollsBackSourceConstructionErrors(t *testing.T) {
	watchErr := errors.New("watch failed")
	loadErr := errors.New("load failed")
	stopErr := errors.New("stop failed")

	t.Run("nil source", func(t *testing.T) {
		if _, _, err := config.NewManager(config.Sources{nil}); err == nil || !strings.Contains(err.Error(), "source is nil") {
			t.Fatalf("NewManager nil source error = %v", err)
		}
	})
	t.Run("watch error", func(t *testing.T) {
		source := newJSONSource(`{}`)
		source.watchErr = watchErr
		if _, _, err := config.NewManager(config.Sources{source}); !errors.Is(err, watchErr) {
			t.Fatalf("NewManager watch error = %v", err)
		}
	})
	t.Run("nil watcher", func(t *testing.T) {
		source := newJSONSource(`{}`)
		source.watcher = nil
		if _, _, err := config.NewManager(config.Sources{source}); err == nil || !strings.Contains(err.Error(), "watcher is nil") {
			t.Fatalf("NewManager nil watcher error = %v", err)
		}
	})
	t.Run("load and stop errors", func(t *testing.T) {
		source := newJSONSource(`{}`)
		source.loadErr = loadErr
		source.watcher.stopErr = stopErr
		if _, _, err := config.NewManager(config.Sources{source}); !errors.Is(err, loadErr) || !errors.Is(err, stopErr) {
			t.Fatalf("NewManager error = %v, want load and stop errors", err)
		}
	})
	t.Run("snapshot and stop errors", func(t *testing.T) {
		source := newJSONSource(`{"feature":`)
		source.watcher.stopErr = stopErr
		if _, _, err := config.NewManager(config.Sources{source}); err == nil ||
			!strings.Contains(err.Error(), "initial config snapshot") || !errors.Is(err, stopErr) {
			t.Fatalf("NewManager error = %v, want snapshot and stop errors", err)
		}
	})
}

func TestManagerCoversChangeBetweenWatchRegistrationAndInitialLoad(t *testing.T) {
	source := newJSONSource(`{"feature":{"value":"old"}}`)
	source.watchHook = func(source *testSource) {
		source.setValues(jsonValues(`{"feature":{"value":"new"}}`))
	}

	manager, cleanup, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	var feature struct {
		Value string `json:"value"`
	}
	if err := manager.Load("feature", &feature); err != nil {
		t.Fatal(err)
	}
	if feature.Value != "new" {
		t.Fatalf("startup value = %q, want new", feature.Value)
	}
	if source.watchCalls.Load() == 0 || source.loadCalls.Load() == 0 {
		t.Fatal("Manager did not drive both Watch and Load")
	}
}

func TestManagerReloadsCompleteSourceAfterDeltaNotification(t *testing.T) {
	source := &testSource{
		values: []*config.KeyValue{
			{Key: "feature.first", Value: []byte("one")},
			{Key: "feature.second", Value: []byte("two")},
		},
		watcher: newTestWatcher(),
	}
	manager, cleanup, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	type featureConfig struct {
		First  string `json:"first"`
		Second string `json:"second"`
	}
	events := make(chan observedValue, 2)
	cancel, err := manager.Subscribe(
		"feature",
		new(featureConfig),
		func(_ string, value any, err error) {
			events <- observedValue{value: value, err: err}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	receiveObservedValue(t, events)

	source.publish(
		[]*config.KeyValue{
			{Key: "feature.first", Value: []byte("updated")},
			{Key: "feature.second", Value: []byte("two")},
		},
		[]*config.KeyValue{{Key: "feature.first", Value: []byte("updated")}},
	)
	updated := receiveObservedValue(t, events)
	if updated.err != nil {
		t.Fatal(updated.err)
	}
	feature := updated.value.(*featureConfig)
	if feature.First != "updated" || feature.Second != "two" {
		t.Fatalf("updated feature = %+v, want updated/two", feature)
	}
}

func TestManagerPreservesSourcePriorityAndRestoresLowerSource(t *testing.T) {
	base := newJSONSource(`{"feature":{"value":"base","baseOnly":true}}`)
	override := newJSONSource(`{"feature":{"value":"override"}}`)
	manager, cleanup, err := config.NewManager(config.Sources{base, override})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	type feature struct {
		Value    string `json:"value"`
		BaseOnly bool   `json:"baseOnly"`
	}
	events := make(chan observedValue, 2)
	cancel, err := manager.Subscribe("feature", new(feature), func(_ string, value any, err error) {
		events <- observedValue{value: value, err: err}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	initial := receiveObservedValue(t, events).value.(*feature)
	if initial.Value != "override" || !initial.BaseOnly {
		t.Fatalf("initial feature = %+v", initial)
	}

	override.publish(jsonValues(`{}`), nil)
	restored := receiveObservedValue(t, events)
	if restored.err != nil {
		t.Fatal(restored.err)
	}
	value := restored.value.(*feature)
	if value.Value != "base" || !value.BaseOnly {
		t.Fatalf("restored feature = %+v", value)
	}
}
