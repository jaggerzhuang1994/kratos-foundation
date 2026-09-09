package testconfig

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

func TestNewBuildsLoadableManagerSnapshot(t *testing.T) {
	want := &config_pb.App{Metadata: map[string]string{"region": "hk"}}
	manager := New(t, "app", want)

	got := new(config_pb.App)
	if err := manager.Load("app", got); err != nil {
		t.Fatal(err)
	}
	if got.GetMetadata()["region"] != "hk" {
		t.Fatalf("loaded metadata = %v", got.GetMetadata())
	}

	want.Metadata["region"] = "changed"
	again := new(config_pb.App)
	if err := manager.Load("app", again); err != nil {
		t.Fatal(err)
	}
	if again.GetMetadata()["region"] != "hk" {
		t.Fatalf("manager retained caller mutation: %v", again.GetMetadata())
	}
}

func TestMutableSourcePublishesCopiesAndStopsWatcher(t *testing.T) {
	source := NewMutableSource(
		t,
		"app",
		&config_pb.App{Metadata: map[string]string{"version": "one"}},
	)

	first, err := source.Load()
	if err != nil {
		t.Fatal(err)
	}
	first[0].Value[0] = 'x'
	again, err := source.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(again[0].Value), `"version":"one"`) {
		t.Fatalf("Load returned aliased bytes: %s", again[0].Value)
	}

	watcher, err := source.Watch()
	if err != nil {
		t.Fatal(err)
	}
	source.Update(t, &config_pb.App{Metadata: map[string]string{"version": "two"}})
	updated, err := watcher.Next()
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != 1 || !strings.Contains(string(updated[0].Value), `"version":"two"`) {
		t.Fatalf("watch update = %#v", updated)
	}
	if err := watcher.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := watcher.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if _, err := watcher.Next(); err != context.Canceled {
		t.Fatalf("Next after Stop error = %v, want context.Canceled", err)
	}
}

func TestEmptyProvidesNotFoundAndDefaultManagerContracts(t *testing.T) {
	manager := Empty(t)
	if manager == nil {
		t.Fatal("Empty returned a nil manager")
	}

	target := new(config_pb.App)
	if err := manager.Load("app", target); !errors.Is(err, config.ErrNotFound) {
		t.Fatalf("empty manager Load error = %v, want ErrNotFound", err)
	}
	if cancel, err := manager.Subscribe(
		"app",
		new(config_pb.App),
		func(string, any, error) { t.Fatal("missing value unexpectedly notified observer") },
	); !errors.Is(err, config.ErrNotFound) || cancel != nil {
		t.Fatalf("empty manager Subscribe = (cancel nil=%t, %v), want (true, ErrNotFound)", cancel == nil, err)
	}

	defaultApp := &config_pb.App{Metadata: map[string]string{"environment": "test"}}
	if err := manager.Load("app", target, defaultApp); err != nil {
		t.Fatal(err)
	}
	if target.GetMetadata()["environment"] != "test" || target == defaultApp {
		t.Fatalf("default Load target = %#v, default = %#v", target, defaultApp)
	}

	callbackCount := 0
	var callbackValue *config_pb.App
	cancel, err := manager.Subscribe(
		"app",
		new(config_pb.App),
		func(key string, value any, callbackErr error) {
			callbackCount++
			if key != "app" || callbackErr != nil {
				t.Fatalf("Subscribe callback = (%q, %#v, %v)", key, value, callbackErr)
			}
			var ok bool
			callbackValue, ok = value.(*config_pb.App)
			if !ok {
				t.Fatalf("Subscribe value type = %T", value)
			}
		},
		defaultApp,
	)
	if err != nil || cancel == nil {
		t.Fatalf("Subscribe with default = (cancel nil=%t, %v)", cancel == nil, err)
	}
	if callbackCount != 1 || callbackValue == nil ||
		callbackValue.GetMetadata()["environment"] != "test" || callbackValue == defaultApp {
		t.Fatalf("synchronous default replay = count %d, value %#v", callbackCount, callbackValue)
	}
	cancel()
	cancel()
}
