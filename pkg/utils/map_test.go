package utils

import (
	"reflect"
	"slices"
	"testing"
)

func TestMapTransforms(t *testing.T) {
	input := map[string]int{"a": 1, "b": 2, "z": 0}
	keys, values := MKeys(input), MValues(input)
	slices.Sort(keys)
	slices.Sort(values)
	if !reflect.DeepEqual(keys, []string{"a", "b", "z"}) || !reflect.DeepEqual(values, []int{0, 1, 2}) {
		t.Fatal(keys, values)
	}
	if got := MMap(input, func(v int) int { return v * 2 }); !reflect.DeepEqual(got, map[string]int{"a": 2, "b": 4, "z": 0}) {
		t.Fatal(got)
	}
	if got := MMapTo(input, true); !reflect.DeepEqual(got, map[string]bool{"a": true, "b": true, "z": true}) {
		t.Fatal(got)
	}
	if got := MMapToAny(input); !reflect.DeepEqual(got, map[string]any{"a": 1, "b": 2, "z": 0}) {
		t.Fatal(got)
	}
	if got := MFilter(input, func(v int) bool { return v > 1 }); !reflect.DeepEqual(got, map[string]int{"b": 2}) {
		t.Fatal(got)
	}
	if got := MFilterZero(input); !reflect.DeepEqual(got, map[string]int{"a": 1, "b": 2}) {
		t.Fatal(got)
	}
	sum := 0
	same := MEach(input, func(v int) { sum += v })
	same["c"] = 3
	if sum != 3 || input["c"] != 3 {
		t.Fatal("MEach must return original map")
	}
	copyMap := Clone(input)
	copyMap["a"] = 9
	if input["a"] != 1 {
		t.Fatal("Clone modified source")
	}
	if Clone[string, int](nil) == nil || MValues[string, int](nil) == nil {
		t.Fatal("nil normalization changed")
	}
}

func TestMapLookup(t *testing.T) {
	input := map[string]int{"a": 1, "b": 2}
	if !MIncludes(input, 1) || MIncludes(input, 3) {
		t.Fatal("unexpected includes")
	}
	if found, key := MFind(input, func(v int) bool { return v == 2 }); !found || key != "b" {
		t.Fatal(found, key)
	}
	if found, key := MFind(input, func(v int) bool { return v == 3 }); found || key != "" {
		t.Fatal(found, key)
	}
	if got := MKeyBy(input, func(v int) int { return v * 10 }); !reflect.DeepEqual(got, map[int]int{10: 1, 20: 2}) {
		t.Fatal(got)
	}
	collision := MKeyBy(input, func(int) int { return 0 })
	if len(collision) != 1 || (collision[0] != 1 && collision[0] != 2) {
		t.Fatal(collision)
	}
}

func TestMapSelection(t *testing.T) {
	for _, tt := range []struct {
		name       string
		input      map[string]int
		keys       []string
		pick, omit map[string]int
	}{
		{"nil", nil, []string{"missing"}, map[string]int{}, map[string]int{}},
		{"no keys", map[string]int{"a": 1}, nil, map[string]int{}, map[string]int{"a": 1}},
		{"present zero and missing", map[string]int{"a": 0, "b": 2}, []string{"a", "a", "missing"}, map[string]int{"a": 0}, map[string]int{"b": 2}},
		{"all keys", map[string]int{"a": 1}, []string{"a"}, map[string]int{"a": 1}, map[string]int{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			picked, omitted := MPick(tt.input, tt.keys...), MOmit(tt.input, tt.keys...)
			if !reflect.DeepEqual(picked, tt.pick) || !reflect.DeepEqual(omitted, tt.omit) {
				t.Fatalf("pick=%v omit=%v", picked, omitted)
			}
			picked["new"] = 3
			omitted["new"] = 4
			if _, changed := tt.input["new"]; changed {
				t.Fatal("selection shares map storage with input")
			}
		})
	}
}
