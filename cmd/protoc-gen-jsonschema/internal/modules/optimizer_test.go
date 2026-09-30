package modules

import (
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/jsonschema"
	pgs "github.com/lyft/protoc-gen-star/v2"
	"strings"
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
	if _, err := optimizer.checkAndMarkSchemaToVisitable(registry, "root"); err != nil {
		t.Fatal(err)
	}
	if err := optimizer.visitSchema(registry, root); err != nil {
		t.Fatal(err)
	}
	optimizer.optimizeDefinitions(registry)
	if !registry.HasSchema("root") || !registry.HasSchema("linked") || registry.HasSchema("orphan") {
		t.Fatalf("optimized keys = %#v", registry.GetKeys())
	}
}

func TestOptimizerReturnsMissingSchemaErrors(t *testing.T) {
	ast := pgs.ProcessCodeGeneratorRequest(pgs.InitMockDebugger(), contractRequest(t, ""))
	entity, ok := ast.Lookup(".contract.v1.Config")
	if !ok {
		t.Fatal("entrypoint message not found")
	}
	entrypoint := entity.(pgs.Message)
	tests := []struct {
		name string
		root *jsonschema.Schema
		want string
	}{
		{name: "hidden entrypoint", want: ".contract.v1.Config"},
		{name: "hidden message", root: &jsonschema.Schema{Ref: ".hidden.Child"}, want: ".hidden.Child"},
		{name: "hidden repeated message", root: &jsonschema.Schema{Items: &jsonschema.Schema{Ref: ".hidden.Child"}}, want: ".hidden.Child"},
		{name: "hidden map value", root: &jsonschema.Schema{AdditionalProperties: &jsonschema.Schema{Ref: ".hidden.Child"}}, want: ".hidden.Child"},
		{name: "hidden nullable message", root: &jsonschema.Schema{OneOf: []*jsonschema.Schema{{Type: "null"}, {Ref: ".hidden.Child"}}}, want: ".hidden.Child"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := jsonschema.NewRegistry()
			if test.root != nil {
				registry.AddSchema(entrypoint.FullyQualifiedName(), test.root)
			}
			err := NewOptimizerImpl().Optimize(registry, entrypoint)
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "visibility_level") {
				t.Fatalf("Optimize error = %v, want missing %s and visibility guidance", err, test.want)
			}
		})
	}
}
