package config

import (
	"fmt"
	"reflect"
	"testing"
)

func TestChangedPaths(t *testing.T) {
	for _, tc := range []struct {
		name, key       string
		before, after   any
		existed, exists bool
		want            []string
	}{
		{name: "nested changed leaf", before: map[string]any{"job": map[string]any{"disabled": false}}, after: map[string]any{"job": map[string]any{"disabled": true}}, existed: true, exists: true, want: []string{"/job/disabled"}},
		{name: "subscription prefix", key: "job.cron", before: map[string]any{"refresh": map[string]any{"disabled": false}}, after: map[string]any{"refresh": map[string]any{"disabled": true}}, existed: true, exists: true, want: []string{"/job/cron/refresh/disabled"}},
		{name: "added null", key: "job", exists: true, want: []string{"/job"}},
		{name: "removed null", key: "job", existed: true, want: []string{"/job"}},
		{name: "unchanged null", key: "job", existed: true, exists: true, want: []string{}},
		{name: "root type change", before: map[string]any{}, after: nil, existed: true, exists: true, want: []string{"<root>"}},
		{name: "array values hidden", key: "clients", before: []any{"secret-one"}, after: []any{"secret-two"}, existed: true, exists: true, want: []string{"/clients"}},
		{name: "sorted add remove and escaped keys", before: map[string]any{"z": 1, "a/b~c": false}, after: map[string]any{"b": 2, "a/b~c": true}, existed: true, exists: true, want: []string{"/a~1b~0c", "/b", "/z"}},
		{name: "unchanged maps", before: map[string]any{"job": true}, after: map[string]any{"job": true}, existed: true, exists: true, want: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated := changedPaths(tc.key, tc.before, tc.existed, tc.after, tc.exists)
			if truncated || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("paths=%v truncated=%v, want=%v", got, truncated, tc.want)
			}
		})
	}
	for _, size := range []int{32, 33} {
		t.Run(fmt.Sprintf("limit_%d", size), func(t *testing.T) {
			values := map[string]any{}
			for i := range size {
				values[fmt.Sprintf("field%02d", i)] = "secret"
			}
			paths, truncated := changedPaths("", map[string]any{}, true, values, true)
			if len(paths) != 32 || truncated != (size > 32) || paths[0] != "/field00" || paths[31] != "/field31" {
				t.Fatalf("paths=%v truncated=%v", paths, truncated)
			}
		})
	}
}
