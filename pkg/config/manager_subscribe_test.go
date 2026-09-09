package config_test

import (
	"errors"
	"math"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
)

func TestManagerSubscribeReplaysThenPublishesFreshValues(t *testing.T) {
	source := newJSONSource(`{"feature":{"value":1}}`)
	manager, cleanup, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	type featureConfig struct {
		Value int `json:"value"`
	}
	events := make(chan observedValue, 3)
	prototype := new(featureConfig)
	cancel, err := manager.Subscribe(
		"feature",
		prototype,
		func(_ string, value any, err error) {
			events <- observedValue{value: value, err: err}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)

	initial := receiveObservedValue(t, events)
	if initial.err != nil {
		t.Fatal(initial.err)
	}
	initialValue := initial.value.(*featureConfig)
	if initialValue == prototype || initialValue.Value != 1 {
		t.Fatalf("initial value = %#v, prototype = %p", initialValue, prototype)
	}

	source.publish(
		jsonValues(`{"feature":{"value":2}}`),
		jsonValues(`{"feature":{"value":2}}`),
	)
	updated := receiveObservedValue(t, events)
	if updated.err != nil {
		t.Fatal(updated.err)
	}
	updatedValue := updated.value.(*featureConfig)
	if updatedValue == initialValue || updatedValue.Value != 2 {
		t.Fatalf("updated value = %#v, initial = %p", updatedValue, initialValue)
	}
}

func TestManagerCancelAndCleanupAreIdempotent(t *testing.T) {
	source := newJSONSource(`{"feature":{"value":1}}`)
	manager, cleanup, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	cancel, err := manager.Subscribe(
		"feature",
		new(struct {
			Value int `json:"value"`
		}),
		func(string, any, error) {},
	)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	cancel()
	cleanup()
	cleanup()

	if got := source.watcher.stopCalls.Load(); got != 1 {
		t.Fatalf("watcher Stop calls = %d, want 1", got)
	}
	if err := manager.Load("feature", new(struct{})); !errors.Is(err, config.ErrManagerClosed) {
		t.Fatalf("Load after cleanup error = %v, want ErrManagerClosed", err)
	}
}

func TestManagerUsesDefaultWhenSubscribedKeyIsDeleted(t *testing.T) {
	source := newJSONSource(`{}`)
	manager, cleanup, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	type feature struct {
		Limit int `json:"limit"`
	}
	events := make(chan observedValue, 3)
	cancel, err := manager.Subscribe(
		"feature",
		new(feature),
		func(_ string, value any, err error) {
			events <- observedValue{value: value, err: err}
		},
		&feature{Limit: 9},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	if got := receiveObservedValue(t, events).value.(*feature).Limit; got != 9 {
		t.Fatalf("initial default = %d, want 9", got)
	}
	source.publish(jsonValues(`{"feature":{"limit":2}}`), nil)
	if got := receiveObservedValue(t, events).value.(*feature).Limit; got != 2 {
		t.Fatalf("configured limit = %d, want 2", got)
	}
	source.publish(jsonValues(`{}`), nil)
	deleted := receiveObservedValue(t, events)
	if deleted.err != nil || deleted.value.(*feature).Limit != 9 {
		t.Fatalf("deleted key event = %+v", deleted)
	}
}

func TestManagerReportsDecodeErrorAndRecovers(t *testing.T) {
	source := newJSONSource(`{"feature":{"value":1}}`)
	manager, cleanup, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	type feature struct {
		Value int `json:"value"`
	}
	events := make(chan observedValue, 3)
	cancel, err := manager.Subscribe("feature", new(feature), func(_ string, value any, err error) {
		events <- observedValue{value: value, err: err}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	receiveObservedValue(t, events)

	source.publish(jsonValues(`{"feature":"invalid"}`), nil)
	if failed := receiveObservedValue(t, events); failed.err == nil {
		t.Fatal("invalid target shape did not report decode error")
	}
	source.publish(jsonValues(`{"feature":{"value":2}}`), nil)
	recovered := receiveObservedValue(t, events)
	if recovered.err != nil || recovered.value.(*feature).Value != 2 {
		t.Fatalf("recovered event = %+v", recovered)
	}
}

func TestManagerIsolatesObserverPanic(t *testing.T) {
	source := newJSONSource(`{"feature":{"value":1}}`)
	manager, cleanup, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	type feature struct {
		Value int `json:"value"`
	}
	cancelPanic, err := manager.Subscribe("feature", new(feature), func(string, any, error) {
		panic("observer panic")
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancelPanic)
	events := make(chan observedValue, 2)
	cancelHealthy, err := manager.Subscribe("feature", new(feature), func(_ string, value any, err error) {
		events <- observedValue{value: value, err: err}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancelHealthy)
	receiveObservedValue(t, events)
	source.publish(jsonValues(`{"feature":{"value":2}}`), nil)
	updated := receiveObservedValue(t, events)
	if updated.err != nil || updated.value.(*feature).Value != 2 {
		t.Fatalf("healthy observer event = %+v", updated)
	}
}

func TestManagerReportsObserverOverload(t *testing.T) {
	source := newJSONSource(`{"feature":{"value":0}}`)
	manager, cleanup, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	type feature struct {
		Value int `json:"value"`
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	errorsSeen := make(chan error, 32)
	var blockOnce sync.Once
	cancel, err := manager.Subscribe("feature", new(feature), func(_ string, value any, err error) {
		if err != nil {
			errorsSeen <- err
			return
		}
		if value.(*feature).Value == 1 {
			blockOnce.Do(func() {
				close(entered)
				<-release
			})
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)

	source.publishAndWaitForReload(jsonValues(`{"feature":{"value":1}}`))
	<-entered
	for value := 2; value <= 20; value++ {
		source.publishAndWaitForReload(jsonValues("{\"feature\":{\"value\":" + strconv.Itoa(value) + "}}"))
	}
	close(release)
	deadline := time.After(time.Second)
	for {
		select {
		case observerErr := <-errorsSeen:
			if errors.Is(observerErr, config.ErrObserverOverloaded) {
				return
			}
		case <-deadline:
			t.Fatal("observer did not receive ErrObserverOverloaded")
		}
	}
}

func TestManagerSubscriptionPreservesIntegerPrecisionAndDefaultFallback(t *testing.T) {
	source := newJSONSource(`{"limits":{"signed":9007199254740993,"unsigned":18446744073709551615}}`)
	manager, cleanup, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	defaults := &integerConfig{Signed: math.MinInt64, Unsigned: math.MaxUint64, Nested: []int64{math.MaxInt64}}
	events := make(chan observedValue, 3)
	cancel, err := manager.Subscribe("limits", new(integerConfig), func(_ string, value any, err error) {
		events <- observedValue{value: value, err: err}
	}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	for index, want := range []*integerConfig{
		{Signed: 9007199254740993, Unsigned: math.MaxUint64, Nested: []int64{math.MaxInt64}},
		{Signed: math.MaxInt64, Unsigned: 9007199254740993, Nested: []int64{math.MinInt64}},
		defaults,
	} {
		if index == 1 {
			source.publish(jsonValues(`{"limits":{"signed":9223372036854775807,"unsigned":9007199254740993,"nested":[-9223372036854775808]}}`), nil)
		}
		if index == 2 {
			source.publish(jsonValues(`{}`), nil)
		}
		event := receiveObservedValue(t, events)
		if event.err != nil {
			t.Fatal(event.err)
		}
		if got := event.value.(*integerConfig); !reflect.DeepEqual(got, want) {
			t.Fatalf("event %d = %+v, want %+v", index, got, want)
		}
	}
}

func TestRemovedFieldUpdateKeepsLastSnapshotAndRecovers(t *testing.T) {
	source := newJSONSource(`{"business":{"enabled":true}}`)
	manager, cleanup, err := config.NewManager(config.NewSources(source))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	events := make(chan observedValue, 4)
	cancel, err := manager.Subscribe("business", new(map[string]bool), func(_ string, value any, err error) { events <- observedValue{value, err} })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	_ = receiveObservedValue(t, events)
	source.publish(jsonValues(`{"business":{"enabled":false},"job":{}}`), nil)
	if event := receiveObservedValue(t, events); event.err == nil {
		t.Fatal("removed field update accepted")
	}
	var current map[string]bool
	if err := manager.Load("business", &current); err != nil || !current["enabled"] {
		t.Fatalf("lost valid snapshot: %v %v", current, err)
	}
	source.publish(jsonValues(`{"business":{"enabled":false}}`), nil)
	if event := receiveObservedValue(t, events); event.err != nil || (*event.value.(*map[string]bool))["enabled"] {
		t.Fatalf("did not recover: %+v", event)
	}
}
