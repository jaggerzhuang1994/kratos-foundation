package modules

import (
	"encoding/json"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/jsonschema"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/proto"
	pgs "github.com/lyft/protoc-gen-star/v2"
	googleproto "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestKubernetesWellKnownMessagesReplaceEmbeddedFields(t *testing.T) {
	tests := []struct {
		messageName string
		fieldName   string
		wantRef     string
	}{
		{messageName: "Volume", fieldName: "volumeSource", wantRef: ".k8s.io.api.core.v1.VolumeSource"},
		{messageName: "SecretProjection", fieldName: "localObjectReference", wantRef: ".k8s.io.api.core.v1.LocalObjectReference"},
		{messageName: "ConfigMapVolumeSource", fieldName: "localObjectReference", wantRef: ".k8s.io.api.core.v1.LocalObjectReference"},
		{messageName: "ConfigMapProjection", fieldName: "localObjectReference", wantRef: ".k8s.io.api.core.v1.LocalObjectReference"},
		{messageName: "ConfigMapKeySelector", fieldName: "localObjectReference", wantRef: ".k8s.io.api.core.v1.LocalObjectReference"},
		{messageName: "SecretKeySelector", fieldName: "localObjectReference", wantRef: ".k8s.io.api.core.v1.LocalObjectReference"},
		{messageName: "ConfigMapEnvSource", fieldName: "localObjectReference", wantRef: ".k8s.io.api.core.v1.LocalObjectReference"},
		{messageName: "SecretEnvSource", fieldName: "localObjectReference", wantRef: ".k8s.io.api.core.v1.LocalObjectReference"},
		{messageName: "Probe", fieldName: "handler", wantRef: ".k8s.io.api.core.v1.ProbeHandler"},
		{messageName: "EphemeralContainer", fieldName: "ephemeralContainerCommon", wantRef: ".k8s.io.api.core.v1.EphemeralContainerCommon"},
	}

	messages := make([]*descriptorpb.DescriptorProto, 0, len(tests))
	for _, test := range tests {
		messages = append(messages, &descriptorpb.DescriptorProto{
			Name: googleproto.String(test.messageName),
			Field: []*descriptorpb.FieldDescriptorProto{
				{
					Name:   googleproto.String(test.fieldName),
					Number: googleproto.Int32(1),
					Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				},
			},
		})
	}
	request := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"k8s.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			{
				Name:        googleproto.String("k8s.proto"),
				Package:     googleproto.String("k8s.io.api.core.v1"),
				Syntax:      googleproto.String("proto3"),
				MessageType: messages,
			},
		},
	}
	ast := pgs.ProcessCodeGeneratorRequest(pgs.InitMockDebugger(), request)
	pluginOptions := &proto.PluginOptions{
		PreserveProtoFieldNames: true,
		AdditionalProperties:    proto.PluginAdditionalProperties_DoNothing,
	}

	for _, test := range tests {
		t.Run(test.messageName, func(t *testing.T) {
			entity, ok := ast.Lookup(".k8s.io.api.core.v1." + test.messageName)
			if !ok {
				t.Fatalf("message %s not found", test.messageName)
			}
			schema := buildFromWellKnownMessage(pluginOptions, entity.(pgs.Message), nil)
			if len(schema.OneOf) != 1 || schema.OneOf[0].Ref != jsonschema.RefId(test.wantRef) {
				t.Errorf("%s oneOf = %#v, want ref %s", test.messageName, schema.OneOf, test.wantRef)
			}
			if _, found := schema.Properties.Get(test.fieldName); found {
				t.Errorf("%s retained embedded property %s", test.messageName, test.fieldName)
			}
			for _, required := range schema.Required {
				if required == test.fieldName {
					t.Errorf("%s retained embedded required property %s", test.messageName, test.fieldName)
				}
			}
		})
	}
}

func TestMapWellKnownValuesUseProtoJSONTypes(t *testing.T) {
	for _, tc := range []struct {
		name, protoName, jsonType, format, pattern string
		enum                                       bool
	}{
		{name: "timestamp", protoName: ".google.protobuf.Timestamp", jsonType: "string", format: "date-time"},
		{name: "duration", protoName: ".google.protobuf.Duration", jsonType: "string", pattern: `^-?[0-9]+(\.[0-9]{1,9})?s$`},
		{name: "any", protoName: ".google.protobuf.Any", jsonType: "object"},
		{name: "null", protoName: ".google.protobuf.NullValue", jsonType: "null", enum: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := contractRequest(t, "draft=Draft202012")
			var root *descriptorpb.DescriptorProto
			for _, file := range request.ProtoFile {
				if file.GetName() == "contract.proto" {
					root = file.MessageType[1]
				}
			}
			typ := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
			if tc.enum {
				typ = descriptorpb.FieldDescriptorProto_TYPE_ENUM
			}
			root.NestedType = append(root.NestedType, typedMapEntry("WellKnownEntry", typ, tc.protoName))
			root.Field = append(root.Field, messageField("well_known", 100, descriptorpb.FieldDescriptorProto_LABEL_REPEATED, ".contract.v1.Config.WellKnownEntry"))
			response := runGenerator(t, request)
			if response.GetError() != "" || len(response.File) != 1 {
				t.Fatalf("response: %v", response)
			}
			var document map[string]any
			if err := json.Unmarshal([]byte(response.File[0].GetContent()), &document); err != nil {
				t.Fatal(err)
			}
			definitions := document["$defs"].(map[string]any)
			field := definitions[".contract.v1.Config.well_known"].(map[string]any)
			value := field["additionalProperties"].(map[string]any)
			if value["type"] != tc.jsonType || value["$ref"] != nil {
				t.Fatalf("map value = %v", value)
			}
			if tc.format != "" && value["format"] != tc.format {
				t.Fatalf("format = %v", value)
			}
			if tc.pattern != "" && value["pattern"] != tc.pattern {
				t.Fatalf("pattern = %v", value)
			}
		})
	}
}
