package jsonschema

import "testing"

func TestDeepCopyRegistryDetachesSchemasAndKeepsOrder(t *testing.T) {
	booleanSchema := true
	registry := NewRegistry()
	registry.AddSchema("z", &Schema{
		Title:           "z",
		Properties:      NewOrderedSchemaMap(),
		Required:        []string{"one"},
		Examples:        []any{"example"},
		Extras:          map[string]any{"x": "y"},
		IsBooleanSchema: &booleanSchema,
	})
	registry.AddSchema("a", &Schema{Title: "a"})
	copy := DeepCopyRegistry(registry)
	cloned := copy.GetSchema("z")
	if len(cloned.Examples) != 1 || cloned.Examples[0] != "example" || cloned.IsBooleanSchema == nil || !*cloned.IsBooleanSchema {
		t.Fatalf("copied schema lost values: %#v", cloned)
	}
	cloned.Title = "changed"
	cloned.Required[0] = "changed"
	cloned.Examples[0] = "changed"
	cloned.Extras["x"] = "changed"
	*cloned.IsBooleanSchema = false
	if got := registry.GetSchema("z"); got.Title != "z" || got.Required[0] != "one" || got.Examples[0] != "example" || got.Extras["x"] != "y" || !*got.IsBooleanSchema {
		t.Fatalf("source was aliased: %#v", got)
	}
	registry.SortSchemas()
	keys := registry.GetKeys()
	if len(keys) != 2 || keys[0] != "a" || keys[1] != "z" {
		t.Fatalf("sorted keys = %#v", keys)
	}
	registry.DeleteSchema("a")
	if registry.HasSchema("a") {
		t.Fatal("DeleteSchema left schema present")
	}
}

func TestSchemaExtrasAndReferences(t *testing.T) {
	s := &Schema{}
	s.SetExtrasItem("visible", true)
	if s.GetExtrasItem("visible") != true {
		t.Fatal("extras value not returned")
	}
	s.ClearExtras()
	if s.GetExtrasItem("visible") != nil {
		t.Fatal("ClearExtras retained value")
	}
	if RefId("").IsEmpty() == false || RefId("pkg.Message").String() != "pkg.Message" {
		t.Fatal("RefId behavior is inconsistent")
	}
}
