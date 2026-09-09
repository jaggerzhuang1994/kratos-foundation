package modules

import (
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/proto"
	pgs "github.com/lyft/protoc-gen-star/v2"
	googleproto "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/pluginpb"
	"reflect"
	"testing"
)

func TestFrontendVisitorBuildsCompositeIntermediateSchemasBeforeDraftConversion(t *testing.T) {
	request := contractRequest(t, "preserve_proto_field_names=true,respect_protojson_int64=true")
	debugger := pgs.InitMockDebugger()
	ast := pgs.ProcessCodeGeneratorRequest(debugger, request)
	visitor := NewVisitor(debugger, proto.GetPluginOptions(pgs.ParseParameters(request.GetParameter())))
	for _, pkg := range ast.Packages() {
		if err := pgs.Walk(visitor, pkg); err != nil {
			t.Fatalf("walk package %s: %v", pkg.ProtoName(), err)
		}
	}

	children := visitor.registry.GetSchema(".contract.v1.Config.children")
	if children == nil || children.Type != "array" || children.Items == nil || children.Items.Ref != ".contract.v1.Child" || children.MinItems == nil || *children.MinItems != 1 {
		t.Errorf("repeated message schema = %#v", children)
	}
	states := visitor.registry.GetSchema(".contract.v1.Config.states")
	if states == nil || states.Items == nil || states.Items.Ref != ".contract.v1.State" {
		t.Errorf("repeated enum schema = %#v", states)
	}
	delays := visitor.registry.GetSchema(".contract.v1.Config.delays")
	if delays == nil || delays.Items == nil || delays.Items.Type != "string" || delays.Items.Format != "" {
		t.Errorf("repeated well-known field schema = %#v", delays)
	}
	state := visitor.registry.GetSchema(".contract.v1.State")
	if state == nil || state.Type != "string" || !reflect.DeepEqual(state.Enum, []any{"STATE_UNSPECIFIED", "ready"}) {
		t.Errorf("visited enum schema = %#v", state)
	}
	intOrString := visitor.registry.GetSchema(".k8s.io.apimachinery.pkg.util.intstr.IntOrString")
	if intOrString == nil || len(intOrString.OneOf) != 2 || intOrString.OneOf[0].Type != "string" || intOrString.OneOf[1].Type != "integer" {
		t.Errorf("well-known message schema = %#v", intOrString)
	}
}

func TestVisitEnumReturnsInvalidCustomValueError(t *testing.T) {
	enumOptions := &descriptorpb.EnumOptions{}
	googleproto.SetExtension(enumOptions, proto.E_Enum, &proto.EnumOptions{MappingType: proto.EnumOptions_MapToCustom})
	valueOptions := &descriptorpb.EnumValueOptions{}
	googleproto.SetExtension(valueOptions, proto.E_EnumValue, &proto.EnumValueOptions{CustomValue: &anypb.Any{Value: []byte(`not-json`)}})
	request := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"invalid.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			{
				Name:    googleproto.String("invalid.proto"),
				Package: googleproto.String("invalid.v1"),
				Syntax:  googleproto.String("proto3"),
				EnumType: []*descriptorpb.EnumDescriptorProto{
					{
						Name:    googleproto.String("State"),
						Options: enumOptions,
						Value: []*descriptorpb.EnumValueDescriptorProto{
							{Name: googleproto.String("STATE_UNSPECIFIED"), Number: googleproto.Int32(0), Options: valueOptions},
						},
					},
				},
			},
		},
	}
	debugger := pgs.InitMockDebugger()
	ast := pgs.ProcessCodeGeneratorRequest(debugger, request)
	entity, ok := ast.Lookup(".invalid.v1.State")
	if !ok {
		t.Fatal("enum entity not found")
	}
	visitor := NewVisitor(debugger, &proto.PluginOptions{})
	if _, err := visitor.VisitEnum(entity.(pgs.Enum)); err == nil {
		t.Fatal("VisitEnum accepted malformed custom JSON value")
	}
	if visitor.registry.HasSchema(".invalid.v1.State") {
		t.Fatal("VisitEnum registered a partial schema after decode failure")
	}
}
