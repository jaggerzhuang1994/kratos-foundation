package snapshot

import (
	"reflect"
	"testing"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
)

func TestMergeRecursivelyMergesMapsAndReplacesOtherTypes(t *testing.T) {
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
