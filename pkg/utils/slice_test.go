package utils

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

func TestSliceTransforms(t *testing.T) {
	input := []int{0, 2, 2, 1}
	if got := Unique(input); !reflect.DeepEqual(got, []int{0, 2, 1}) {
		t.Fatal(got)
	}
	if got := Map(input, func(v int) int { return v * 2 }); !reflect.DeepEqual(got, []int{0, 4, 4, 2}) {
		t.Fatal(got)
	}
	if got := MapTo(input, "x"); !reflect.DeepEqual(got, []string{"x", "x", "x", "x"}) {
		t.Fatal(got)
	}
	if got := MapToAny(input); !reflect.DeepEqual(got, []any{0, 2, 2, 1}) {
		t.Fatal(got)
	}
	if got := Filter(input, func(v int) bool { return v > 1 }); !reflect.DeepEqual(got, []int{2, 2}) {
		t.Fatal(got)
	}
	if got := FilterZero(input); !reflect.DeepEqual(got, []int{2, 2, 1}) {
		t.Fatal(got)
	}
	var visited []int
	got := Each(input, func(v int) { visited = append(visited, v) })
	if !reflect.DeepEqual(visited, input) {
		t.Fatal(visited)
	}
	got[0] = 9
	if input[0] != 9 {
		t.Fatal("Each must return original slice")
	}
	if !reflect.DeepEqual(Unique[int](nil), []int{}) || Map[int, int](nil, nil) == nil {
		t.Fatal("empty allocated results changed")
	}
}

func TestSliceLookupAndGrouping(t *testing.T) {
	input := []int{3, 2, 3, 4}
	if !Includes(input, 2) || Includes(input, 0) || Find(input, func(v int) bool { return v == 3 }) != 0 ||
		Find(input, func(v int) bool { return v == 0 }) != -1 ||
		FindItem(input, func(v int) bool { return v%2 == 0 }) != 2 ||
		FindItem(input, func(v int) bool { return v == 0 }) != 0 {
		t.Fatal("unexpected lookup")
	}
	groups := GroupBy(input, func(v int) int { return v % 2 })
	want := GroupItems[int, int]{{Group: 1, Values: []int{3, 3}}, {Group: 0, Values: []int{2, 4}}}
	if !reflect.DeepEqual(groups, want) {
		t.Fatal(groups)
	}
	groupMap := groups.ToMap()
	groupMap[1][0] = 5
	if groups[0].Values[0] != 5 {
		t.Fatal("ToMap must share value slices")
	}
	if got := GroupBy([]int(nil), func(v int) int { return v }); got != nil {
		t.Fatal(got)
	}
	if got := KeyBy(input, func(v int) int { return v % 2 }); !reflect.DeepEqual(got, map[int]int{1: 3, 0: 4}) {
		t.Fatal(got)
	}
	if got := Pluck(input, func(v int) int { return v % 2 }, func(v int) int { return v * 10 }); !reflect.DeepEqual(got, map[int]int{1: 30, 0: 40}) {
		t.Fatal(got)
	}
	for _, tt := range []struct{ a, b, want []int }{
		{[]int{1, 2, 3}, []int{3, 3, 2, 4}, []int{3, 2}},
		{[]int{1}, []int{2}, nil}, {nil, nil, nil},
	} {
		if got := Intersect(tt.a, tt.b); !reflect.DeepEqual(got, tt.want) {
			t.Fatal(got)
		}
	}
}

func TestUniqueBy(t *testing.T) {
	type item struct {
		id    int
		value string
	}
	input := []item{{2, "first"}, {1, "other"}, {2, "later"}}
	got := UniqueBy(input, func(v item) int { return v.id })
	if !reflect.DeepEqual(got, []item{{2, "first"}, {1, "other"}}) {
		t.Fatal(got)
	}
	got[0].value = "changed"
	if input[0].value != "first" {
		t.Fatal("result shares slice storage")
	}
	if got := UniqueBy([]item(nil), func(v item) int { return v.id }); got == nil || len(got) != 0 {
		t.Fatal(got)
	}
}

func TestFilterMap(t *testing.T) {
	var visited []int
	got := FilterMap([]int{2, 0, 1, -1}, func(v int) (string, bool) {
		visited = append(visited, v)
		return fmt.Sprint(v), v >= 0
	})
	if !reflect.DeepEqual(got, []string{"2", "0", "1"}) || !reflect.DeepEqual(visited, []int{2, 0, 1, -1}) {
		t.Fatal(got, visited)
	}
	for _, input := range [][]int{nil, {-2, -1}} {
		if got := FilterMap(input, func(v int) (int, bool) { return v, v >= 0 }); got == nil || len(got) != 0 {
			t.Fatal(got)
		}
	}
}

func TestPartition(t *testing.T) {
	for _, tt := range []struct {
		input, matched, rest []int
	}{
		{nil, []int{}, []int{}},
		{[]int{1, 3}, []int{}, []int{1, 3}},
		{[]int{2, 4}, []int{2, 4}, []int{}},
		{[]int{3, 2, 1, 4}, []int{2, 4}, []int{3, 1}},
	} {
		var visited []int
		matched, rest := Partition(tt.input, func(v int) bool {
			visited = append(visited, v)
			return v%2 == 0
		})
		if !slices.Equal(visited, tt.input) || !reflect.DeepEqual(matched, tt.matched) || !reflect.DeepEqual(rest, tt.rest) {
			t.Fatal(matched, rest, visited)
		}
		if len(matched) > 0 {
			matched[0] = 99
		}
		if len(rest) > 0 {
			rest[0] = 99
		}
		if slices.Contains(tt.input, 99) {
			t.Fatal("partition changed input")
		}
	}
}

func TestDifference(t *testing.T) {
	for _, tt := range []struct{ a, b, want []int }{
		{nil, nil, []int{}},
		{[]int{1, 2}, []int{2, 1}, []int{}},
		{[]int{3, 1, 3, 2, 4}, []int{2, 2}, []int{3, 1, 4}},
		{[]int{3, 3, 1}, nil, []int{3, 1}},
	} {
		got := Difference(tt.a, tt.b)
		if !reflect.DeepEqual(got, tt.want) {
			t.Fatal(got, tt.want)
		}
		if len(got) > 0 {
			got[0] = 99
			if slices.Contains(tt.a, 99) {
				t.Fatal("difference changed input")
			}
		}
	}
}
