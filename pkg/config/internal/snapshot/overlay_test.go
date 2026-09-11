package snapshot

import (
	"fmt"
	"reflect"
	"testing"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
)

func TestSnapshotRebuildDoesNotMutatePreviousSnapshotOrSources(t *testing.T) {
	base := &kratosconfig.KeyValue{Key: "base.json", Format: "json", Value: []byte(`{"service":{"labels":{"env":"test","region":"base"},"items":[{"name":"base"}]}}`)}
	override := &kratosconfig.KeyValue{Key: "override.json", Format: "json", Value: []byte(`{"service":{"labels":{"region":"override"},"items":[]}}`)}
	baseText, overrideText := string(base.Value), string(override.Value)
	previous, err := New([]*kratosconfig.KeyValue{base, override})
	if err != nil {
		t.Fatal(err)
	}
	// 删除高优先级源后应回退到原始低优先级值，不污染已经发布的快照。
	current, err := New([]*kratosconfig.KeyValue{base})
	if err != nil {
		t.Fatal(err)
	}
	region, _ := current.Lookup("service.labels.region")
	oldRegion, _ := previous.Lookup("service.labels.region")
	items, _ := current.Lookup("service.items")
	oldItems, _ := previous.Lookup("service.items")
	if region != "base" || oldRegion != "override" || len(items.([]any)) != 1 || len(oldItems.([]any)) != 0 {
		t.Fatalf("rebuild changed source priority or previous snapshot: %v, %v, %v, %v", region, oldRegion, items, oldItems)
	}
	if string(base.Value) != baseText || string(override.Value) != overrideText {
		t.Fatal("snapshot construction mutated source bytes")
	}
}

func BenchmarkSnapshotBuild(b *testing.B) {
	for _, count := range []int{10, 100, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			values := make([]*kratosconfig.KeyValue, count)
			for i := range values {
				values[i] = &kratosconfig.KeyValue{Key: fmt.Sprintf("%d.json", i), Format: "json", Value: []byte(fmt.Sprintf(`{"key%d":%d}`, i, i))}
			}
			for b.Loop() {
				if _, err := New(values); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestOverlaySourceRecursivelyOverlaysMapsAndReplacesOtherTypes(t *testing.T) {
	t.Parallel()

	current, err := New([]*kratosconfig.KeyValue{
		{
			Key:    "base.json",
			Format: "json",
			Value: []byte(`{
				"service": {
					"labels": {"env": "test", "region": "base"},
					"endpoints": ["base"],
					"nullable": "base"
				}
			}`),
		},
		{
			Key:    "override.json",
			Format: "json",
			Value: []byte(`{
				"service": {
					"labels": {"region": "override"},
					"endpoints": [],
					"nullable": null
				}
			}`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	value, found := current.Lookup("service")
	if !found {
		t.Fatal("service is missing")
	}
	want := map[string]any{
		"labels": map[string]any{
			"env":    "test",
			"region": "override",
		},
		"endpoints": []any{},
		"nullable":  nil,
	}
	if !reflect.DeepEqual(value, want) {
		t.Fatalf("service = %#v, want %#v", value, want)
	}
}
