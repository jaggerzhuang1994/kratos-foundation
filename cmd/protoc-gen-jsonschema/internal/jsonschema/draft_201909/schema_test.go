package draft_201909

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/jsonschema"
)

func TestNewMapsArrayItemsUsingDraft201909Keywords(t *testing.T) {
	assertArrayItemsEncoding(t, &jsonschema.Schema{
		Items: &jsonschema.Schema{Type: "string"},
	}, `"items":{"type":"string"}`, "")
	assertArrayItemsEncoding(t, &jsonschema.Schema{
		PrefixItems: []*jsonschema.Schema{{Type: "string"}},
		Items:       &jsonschema.Schema{Type: "integer"},
	}, `"items":[{"type":"string"}]`, `"additionalItems":{"type":"integer"}`)
}

func assertArrayItemsEncoding(t *testing.T, origin *jsonschema.Schema, wantItems, wantAdditional string) {
	t.Helper()
	encoded, err := json.Marshal(New(origin))
	if err != nil {
		t.Fatal(err)
	}
	content := string(encoded)
	if !strings.Contains(content, wantItems) {
		t.Fatalf("schema = %s, want %s", content, wantItems)
	}
	if wantAdditional == "" && strings.Contains(content, `"additionalItems"`) {
		t.Fatalf("schema = %s, homogeneous items must not use additionalItems", content)
	}
	if wantAdditional != "" && !strings.Contains(content, wantAdditional) {
		t.Fatalf("schema = %s, want %s", content, wantAdditional)
	}
}

func TestNewUsesDefinitionsAndDetachesSource(t *testing.T) {
	origin := &jsonschema.Schema{
		Version:     "v",
		Ref:         "Root",
		Definitions: jsonschema.NewOrderedSchemaMap(),
		Examples:    []any{"original"},
	}
	origin.Definitions.Set("Child", &jsonschema.Schema{Title: "child"})

	schema := New(origin)
	origin.Definitions.Set("Later", &jsonschema.Schema{})
	origin.Examples[0] = "changed"

	if schema.RefID() != "#/$defs/Root" || schema.Schema() != "v" {
		t.Fatalf("conversion = ref %q schema %q", schema.RefID(), schema.Schema())
	}
	if keys := schema.Definitions.Keys(); len(keys) != 1 || keys[0] != "Child" {
		t.Fatalf("definitions = %#v", keys)
	}
	if len(schema.Examples) != 1 || schema.Examples[0] != "original" {
		t.Fatalf("examples = %#v, want detached original", schema.Examples)
	}
}

func TestMergeInitializesMissingDefinitions(t *testing.T) {
	schema := New(&jsonschema.Schema{Ref: "Root"})
	other := New(&jsonschema.Schema{Ref: "External"})

	if err := schema.Merge(other); err != nil {
		t.Fatal(err)
	}
	assertMergeReference(t, schema, ".Merge_0", "#/$defs/Root", "#/$defs/External")
}

func TestMergeRejectsConflictingDefinitions(t *testing.T) {
	originDefinitions := jsonschema.NewOrderedSchemaMap()
	originDefinitions.Set("Child", &jsonschema.Schema{Title: "original"})
	otherDefinitions := jsonschema.NewOrderedSchemaMap()
	otherDefinitions.Set("Added", &jsonschema.Schema{Title: "must not leak"})
	otherDefinitions.Set("Child", &jsonschema.Schema{Title: "replacement"})

	schema := New(&jsonschema.Schema{Ref: "Root", Definitions: originDefinitions})
	other := New(&jsonschema.Schema{Ref: "External", Definitions: otherDefinitions})
	err := schema.Merge(other)
	if err == nil || !strings.Contains(err.Error(), `definition "Child" conflicts`) {
		t.Fatalf("Merge() error = %v", err)
	}
	if _, exists := schema.Definitions.Get("Added"); exists {
		t.Fatal("failed merge left a partially added definition")
	}
}

func TestMergePreservesExternalRootConstraints(t *testing.T) {
	schema := New(&jsonschema.Schema{Ref: "Root"})
	definitions := jsonschema.NewOrderedSchemaMap()
	definitions.Set("External", &jsonschema.Schema{Type: "object"})
	other := New(&jsonschema.Schema{Version: "draft", ID: "https://example.test/external", Ref: "External", Type: "object", Required: []string{"name"}, Definitions: definitions})
	if err := schema.Merge(other); err != nil {
		t.Fatal(err)
	}
	value, _ := schema.Definitions.Get(".Merge_0")
	external := value.(*Schema).AllOf[1]
	if external.Ref != "" || len(external.AllOf) == 0 || external.AllOf[0].Ref != "#/$defs/External" || external.ID == "" || external.Type != "object" || len(external.Required) != 1 || external.Version != "" || external.Definitions == nil {
		t.Fatalf("external merge branch = %#v", external)
	}
	if _, exists := schema.Definitions.Get("External"); !exists {
		t.Fatal("external definition was not promoted")
	}
}

func TestMergeRejectsIncompatibleDraftWithAccurateType(t *testing.T) {
	err := New(&jsonschema.Schema{}).Merge(incompatibleDraft{})
	if err == nil || !strings.Contains(err.Error(), "draft_201909.Schema") {
		t.Fatalf("Merge() error = %v", err)
	}
}

func assertMergeReference(t *testing.T, schema *Schema, name, left, right string) {
	t.Helper()
	if schema.RefID() != "#/$defs/"+name {
		t.Fatalf("RefID() = %q", schema.RefID())
	}
	value, ok := schema.Definitions.Get(name)
	merged, typeOK := value.(*Schema)
	if !ok || !typeOK || len(merged.AllOf) != 2 || len(merged.AllOf[1].AllOf) == 0 ||
		merged.AllOf[0].Ref != left || merged.AllOf[1].Ref != "" || merged.AllOf[1].AllOf[0].Ref != right {
		t.Fatalf("merge definition = %#v, found=%t", value, ok)
	}
}

type incompatibleDraft struct{}

func (incompatibleDraft) Schema() string               { return "incompatible" }
func (incompatibleDraft) RefID() string                { return "" }
func (incompatibleDraft) Merge(jsonschema.Draft) error { return nil }
