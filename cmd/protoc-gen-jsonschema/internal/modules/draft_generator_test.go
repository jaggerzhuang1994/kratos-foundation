package modules

import (
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/jsonschema"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/proto"
	pgs "github.com/lyft/protoc-gen-star/v2"
	"testing"
)

func TestMultiDraftGeneratorSelectsEverySupportedDraft(t *testing.T) {
	request := contractRequest(t, "")
	debugger := pgs.InitMockDebugger()
	ast := pgs.ProcessCodeGeneratorRequest(debugger, request)
	entity, ok := ast.Lookup(".contract.v1.Config")
	if !ok {
		t.Fatal("entrypoint message not found")
	}
	entrypoint := entity.(pgs.Message)
	registry := jsonschema.NewRegistry()
	registry.AddSchema(entrypoint.FullyQualifiedName(), &jsonschema.Schema{Type: "object"})

	tests := []struct {
		name       string
		draft      proto.Draft
		wantSchema string
		wantRef    string
	}{
		{name: "draft 04", draft: proto.Draft_Draft04, wantSchema: draft04Version, wantRef: "#/definitions/.contract.v1.Config"},
		{name: "draft 06", draft: proto.Draft_Draft06, wantSchema: draft06Version, wantRef: "#/definitions/.contract.v1.Config"},
		{name: "draft 07", draft: proto.Draft_Draft07, wantSchema: draft07Version, wantRef: "#/definitions/.contract.v1.Config"},
		{name: "draft 2019-09", draft: proto.Draft_Draft201909, wantSchema: draft201909Version, wantRef: "#/$defs/.contract.v1.Config"},
		{name: "draft 2020-12", draft: proto.Draft_Draft202012, wantSchema: draft202012Version, wantRef: "#/$defs/.contract.v1.Config"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			generator := NewMultiDraftGenerator(nil, &proto.PluginOptions{Draft: test.draft})
			got := generator.Generate(registry, entrypoint, &proto.FileOptions{Title: "Root", Description: "Description"})
			if got == nil {
				t.Fatal("Generate() = nil")
			}
			if got.Schema() != test.wantSchema || got.RefID() != test.wantRef {
				t.Errorf("Generate() schema = %q, ref = %q; want %q, %q", got.Schema(), got.RefID(), test.wantSchema, test.wantRef)
			}
		})
	}

	if got := NewMultiDraftGenerator(nil, &proto.PluginOptions{Draft: proto.Draft(99)}).Generate(registry, entrypoint, nil); got != nil {
		t.Errorf("Generate(unsupported draft) = %#v, want nil", got)
	}
}
