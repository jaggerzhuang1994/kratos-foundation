package decoder

import (
	"errors"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"strings"
	"testing"
)

// Foundation 配置不再声明 reserved；业务自定义消息仍可使用此解码约束。
func TestReservedFieldsAreRejectedWithoutRejectingBusinessExtensions(t *testing.T) {
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: proto.String("business.proto"), Syntax: proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Business"), ReservedName: []string{"old_field"},
			Field: []*descriptorpb.FieldDescriptorProto{
				{Name: proto.String("child"), Number: proto.Int32(1), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".Business")},
				{Name: proto.String("children"), Number: proto.Int32(2), Label: descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".Business")},
			},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := file.Messages().Get(0)
	for _, tc := range []struct {
		name  string
		value any
		path  string
	}{
		{"null", map[string]any{"old_field": nil}, "old_field"},
		{"alias", map[string]any{"oldField": false}, "oldField"},
		{"nested", map[string]any{"child": map[string]any{"old_field": true}}, "child.old_field"},
		{"repeated", map[string]any{"children": []any{map[string]any{"old_field": true}}}, "children[0].old_field"},
		{"unknown", map[string]any{"extension": true}, ""},
		{"scalar", "invalid shape handled by decoder", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateReserved(tc.value, descriptor, "")
			if tc.path == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, ErrRemovedField) || !strings.Contains(err.Error(), tc.path) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	target := dynamicpb.NewMessage(descriptor)
	if err := scan(map[string]any{"old_field": "secret"}, target); !errors.Is(err, ErrRemovedField) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe or missing diagnostic: %v", err)
	}
}
