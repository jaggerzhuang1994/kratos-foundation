package modules

import (
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/jsonschema"
	"testing"
)

func TestOptimizerKeepsOnlyEntrypointLinkedDefinitions(t *testing.T) {
	registry := jsonschema.NewRegistry()
	registry.AddSchema("root", &jsonschema.Schema{Properties: jsonschema.NewOrderedSchemaMap()})
	registry.AddSchema("linked", &jsonschema.Schema{})
	registry.AddSchema("orphan", &jsonschema.Schema{})
	root := registry.GetSchema("root")
	root.Properties.Set("child", &jsonschema.Schema{Ref: "linked"})
	optimizer := NewOptimizerImpl()
	optimizer.checkAndMarkSchemaToVisitable(registry, "root")
	optimizer.visitSchema(registry, root)
	optimizer.optimizeDefinitions(registry)
	if !registry.HasSchema("root") || !registry.HasSchema("linked") || registry.HasSchema("orphan") {
		t.Fatalf("optimized keys = %#v", registry.GetKeys())
	}
}
