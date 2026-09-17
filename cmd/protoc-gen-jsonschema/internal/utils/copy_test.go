package utils

import (
	"math"
	"testing"
)

func TestCloneAndCompareJSONValues(t *testing.T) {
	source := map[string]any{"nested": map[string]any{"value": 1}, "items": []any{"a"}}
	cloned, err := CloneJSON(&source)
	if err != nil {
		t.Fatal(err)
	}
	(*cloned)["nested"].(map[string]any)["value"] = float64(2)
	if source["nested"].(map[string]any)["value"] != 1 {
		t.Fatal("CloneJSON shared nested state")
	}
	equal, err := EqualJSON(map[string]any{"a": 1, "b": 2}, map[string]any{"b": 2, "a": 1})
	if err != nil || !equal {
		t.Fatalf("EqualJSON() = %v, %v", equal, err)
	}
	equal, err = EqualJSON(map[string]any{"a": 1}, map[string]any{"a": 2})
	if err != nil || equal {
		t.Fatalf("EqualJSON(different) = %v, %v", equal, err)
	}
	if _, err := CloneJSON(&map[string]any{"invalid": math.Inf(1)}); err == nil {
		t.Fatal("CloneJSON accepted a non-JSON value")
	}
	if _, err := EqualJSON(math.Inf(1), 1); err == nil {
		t.Fatal("EqualJSON accepted a non-JSON value")
	}
}

func TestPointerCopiesHandleNilAndDetachValues(t *testing.T) {
	intValue, floatValue, boolValue := 1, 1.5, true
	anyValue := any("value")

	intCopy := CopyIntP(&intValue)
	floatCopy := CopyFloat64P(&floatValue)
	boolCopy := CopyBoolP(&boolValue)
	anyCopy := CopyAnyP(&anyValue)
	if intCopy == &intValue || floatCopy == &floatValue || boolCopy == &boolValue || anyCopy == &anyValue {
		t.Fatal("pointer helper returned the source pointer")
	}
	*intCopy, *floatCopy, *boolCopy, *anyCopy = 2, 2.5, false, "changed"
	if intValue != 1 || floatValue != 1.5 || !boolValue || anyValue != "value" {
		t.Fatal("mutating pointer copies changed source values")
	}

	if CopyIntP(nil) != nil || CopyFloat64P(nil) != nil || CopyBoolP(nil) != nil || CopyAnyP(nil) != nil {
		t.Fatal("pointer helper converted nil into a value")
	}
}

func TestSliceCopiesHandleNilAndDetachBackingArrays(t *testing.T) {
	stringsSource := []string{"a"}
	stringsCopy := CopyStringArray(stringsSource)
	stringsCopy[0] = "changed"
	if stringsSource[0] != "a" {
		t.Fatal("CopyStringArray shares its backing array")
	}

	anySource := []any{"a"}
	anyCopy := CopyAnyArray(anySource)
	anyCopy[0] = "changed"
	if anySource[0] != "a" {
		t.Fatal("CopyAnyArray shares its backing array")
	}

	if CopyStringArray(nil) != nil || CopyAnyArray(nil) != nil {
		t.Fatal("slice helper converted nil into an empty slice")
	}
	if CopyStringArray([]string{}) == nil || CopyAnyArray([]any{}) == nil {
		t.Fatal("slice helper converted an empty slice into nil")
	}
}

func TestMapCopiesHandleNilAndDetachContainers(t *testing.T) {
	anySource := map[string]any{"key": "value"}
	anyCopy := CopyMapAny(anySource)
	anyCopy["key"] = "changed"
	if anySource["key"] != "value" {
		t.Fatal("CopyMapAny shares its map")
	}

	stringSource := map[string]string{"key": "value"}
	stringCopy := CopyMapString(stringSource)
	stringCopy["key"] = "changed"
	if stringSource["key"] != "value" {
		t.Fatal("CopyMapString shares its map")
	}

	arraySource := map[string][]string{"key": {"value"}}
	arrayCopy := CopyMapStringArray(arraySource)
	arrayCopy["key"][0] = "changed"
	if arraySource["key"][0] != "value" {
		t.Fatal("CopyMapStringArray shares a nested slice")
	}

	if CopyMapAny(nil) != nil || CopyMapString(nil) != nil || CopyMapStringArray(nil) != nil {
		t.Fatal("map helper converted nil into an empty map")
	}
}

func TestPointerConstructorsPreserveValues(t *testing.T) {
	t.Parallel()

	if got := UInt32(42); got == nil || *got != 42 {
		t.Fatalf("UInt32(42) = %v, want pointer to 42", got)
	}
	if got := Int32(-17); got == nil || *got != -17 {
		t.Fatalf("Int32(-17) = %v, want pointer to -17", got)
	}
	if got := Int(23); got == nil || *got != 23 {
		t.Fatalf("Int(23) = %v, want pointer to 23", got)
	}
	if got := Bool(true); got == nil || !*got {
		t.Fatalf("Bool(true) = %v, want pointer to true", got)
	}
	if got := Bool(false); got == nil || *got {
		t.Fatalf("Bool(false) = %v, want pointer to false", got)
	}
}

func TestPointerConstructorsReturnIndependentStorage(t *testing.T) {
	t.Parallel()

	first := Int(7)
	second := Int(7)
	*first = 9
	if *second != 7 {
		t.Fatalf("mutating first pointer changed second to %d", *second)
	}
}
