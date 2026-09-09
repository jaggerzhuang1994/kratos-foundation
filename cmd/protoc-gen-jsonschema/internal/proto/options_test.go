package proto

import (
	"testing"

	pgs "github.com/lyft/protoc-gen-star/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestDescriptorOptionReadersReturnEveryRegisteredExtension(t *testing.T) {
	fileWant := &FileOptions{EntrypointMessage: "Config", Title: "Document"}
	messageWant := &MessageOptions{Title: "Configuration", Object: &ObjectKeywords{MinProperties: proto.Uint32(1)}}
	fieldWant := &FieldOptions{Title: "Name", Nullable: true, String_: &StringKeywords{MinLength: proto.Uint32(2)}}
	enumWant := &EnumOptions{MappingType: EnumOptions_MapToCustom, Title: "State"}
	enumValueWant := &EnumValueOptions{CustomValue: &anypb.Any{Value: []byte(`"ready"`)}}

	fileDescriptorOptions := &descriptorpb.FileOptions{}
	proto.SetExtension(fileDescriptorOptions, E_File, fileWant)
	messageDescriptorOptions := &descriptorpb.MessageOptions{}
	proto.SetExtension(messageDescriptorOptions, E_Message, messageWant)
	fieldDescriptorOptions := &descriptorpb.FieldOptions{}
	proto.SetExtension(fieldDescriptorOptions, E_Field, fieldWant)
	enumDescriptorOptions := &descriptorpb.EnumOptions{}
	proto.SetExtension(enumDescriptorOptions, E_Enum, enumWant)
	enumValueDescriptorOptions := &descriptorpb.EnumValueOptions{}
	proto.SetExtension(enumValueDescriptorOptions, E_EnumValue, enumValueWant)

	request := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"options.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			{
				Name:    proto.String("options.proto"),
				Package: proto.String("options.v1"),
				Syntax:  proto.String("proto3"),
				Options: fileDescriptorOptions,
				EnumType: []*descriptorpb.EnumDescriptorProto{
					{
						Name:    proto.String("State"),
						Options: enumDescriptorOptions,
						Value: []*descriptorpb.EnumValueDescriptorProto{
							{Name: proto.String("STATE_UNSPECIFIED"), Number: proto.Int32(0)},
							{Name: proto.String("READY"), Number: proto.Int32(1), Options: enumValueDescriptorOptions},
						},
					},
					{
						Name: proto.String("PlainState"),
						Value: []*descriptorpb.EnumValueDescriptorProto{
							{Name: proto.String("PLAIN_STATE_UNSPECIFIED"), Number: proto.Int32(0)},
						},
					},
				},
				MessageType: []*descriptorpb.DescriptorProto{
					{
						Name:    proto.String("Config"),
						Options: messageDescriptorOptions,
						Field: []*descriptorpb.FieldDescriptorProto{
							{
								Name:    proto.String("name"),
								Number:  proto.Int32(1),
								Label:   descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
								Type:    descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
								Options: fieldDescriptorOptions,
							},
							{
								Name:   proto.String("plain"),
								Number: proto.Int32(2),
								Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
								Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
							},
						},
					},
					{Name: proto.String("Plain")},
				},
			},
			{Name: proto.String("plain.proto"), Package: proto.String("plain.v1"), Syntax: proto.String("proto3")},
		},
	}
	ast := pgs.ProcessCodeGeneratorRequest(pgs.InitMockDebugger(), request)

	file := lookupEntity[pgs.File](t, ast, "options.proto")
	message := lookupEntity[pgs.Message](t, ast, ".options.v1.Config")
	field := lookupEntity[pgs.Field](t, ast, ".options.v1.Config.name")
	enum := lookupEntity[pgs.Enum](t, ast, ".options.v1.State")
	enumValue := lookupEntity[pgs.EnumValue](t, ast, ".options.v1.State.READY")

	if got := GetFileOptions(file); !proto.Equal(got, fileWant) {
		t.Errorf("GetFileOptions() = %v, want %v", got, fileWant)
	}
	if got := GetMessageOptions(message); !proto.Equal(got, messageWant) {
		t.Errorf("GetMessageOptions() = %v, want %v", got, messageWant)
	}
	if got := GetFieldOptions(field); !proto.Equal(got, fieldWant) {
		t.Errorf("GetFieldOptions() = %v, want %v", got, fieldWant)
	}
	if got := GetEnumOptions(enum); !proto.Equal(got, enumWant) {
		t.Errorf("GetEnumOptions() = %v, want %v", got, enumWant)
	}
	if got := GetEnumValueOptions(enumValue); !proto.Equal(got, enumValueWant) {
		t.Errorf("GetEnumValueOptions() = %v, want %v", got, enumValueWant)
	}

	if got := GetEnumValueOptions(enum.Values()[0]); got != nil {
		t.Errorf("GetEnumValueOptions(without extension) = %v, want nil", got)
	}
	if got := GetMessageOptions(lookupEntity[pgs.Message](t, ast, ".options.v1.Plain")); got != nil {
		t.Errorf("GetMessageOptions(without extension) = %v, want nil", got)
	}
	if got := GetFieldOptions(lookupEntity[pgs.Field](t, ast, ".options.v1.Config.plain")); got != nil {
		t.Errorf("GetFieldOptions(without extension) = %v, want nil", got)
	}
	if got := GetEnumOptions(lookupEntity[pgs.Enum](t, ast, ".options.v1.PlainState")); got != nil {
		t.Errorf("GetEnumOptions(without extension) = %v, want nil", got)
	}
	if got := GetFileOptions(lookupEntity[pgs.File](t, ast, "plain.proto")); got != nil {
		t.Errorf("GetFileOptions(without extension) = %v, want nil", got)
	}
}

func lookupEntity[T pgs.Entity](t *testing.T, ast pgs.AST, name string) T {
	t.Helper()

	entity, ok := ast.Lookup(name)
	if !ok {
		t.Fatalf("AST entity %s not found", name)
	}
	typed, ok := entity.(T)
	if !ok {
		t.Fatalf("AST entity %s has type %T", name, entity)
	}
	return typed
}

func TestGetPluginOptionsAppliesDefaultsAndExplicitOverrides(t *testing.T) {
	defaults := GetPluginOptions(pgs.Parameters{})
	if defaults.GetDraft() != Draft_Draft202012 || defaults.GetOutputFileSuffix() != ".schema.json" || defaults.GetMandatoryNullable() != true || defaults.GetAdditionalProperties() != PluginAdditionalProperties_DoNothing {
		t.Fatalf("unexpected defaults: %#v", defaults)
	}
	overrides := GetPluginOptions(pgs.Parameters{
		"visibility_level":           "3",
		"merge":                      "base.schema.json",
		"entrypoint_message":         "Config",
		"draft":                      "Draft07",
		"output_file_suffix":         ".yaml",
		"mandatory_nullable":         "false",
		"preserve_proto_field_names": "true",
		"additional_properties":      "AlwaysFalse",
		"respect_protojson_presence": "true",
		"respect_protojson_int64":    "true",
	})
	if overrides.GetVisibilityLevel() != 3 || overrides.GetMerge() != "base.schema.json" || overrides.GetEntrypointMessage() != "Config" || overrides.GetDraft() != Draft_Draft07 || overrides.GetOutputFileSuffix() != ".yaml" || overrides.GetMandatoryNullable() || !overrides.GetPreserveProtoFieldNames() || overrides.GetAdditionalProperties() != PluginAdditionalProperties_AlwaysFalse || !overrides.GetRespectProtojsonPresence() || !overrides.GetRespectProtojsonInt64() {
		t.Fatalf("unexpected overrides: %#v", overrides)
	}
}

func TestGetPluginOptionsHonorsLegacyInt64OptionAndAdditionalPropertyPrecedence(t *testing.T) {
	options := GetPluginOptions(pgs.Parameters{"int64_as_string": "true", "respect_protojson_int64": "false"})
	if !options.GetRespectProtojsonInt64() {
		t.Fatal("legacy int64_as_string did not take precedence")
	}
}

func TestTitleAndDescriptionResolvers(t *testing.T) {
	if got := GetTitleOrEmpty(nil); got != "" {
		t.Fatalf("GetTitleOrEmpty(nil) = %q", got)
	}
	if got := GetTitleOrEmpty(&FileOptions{Title: "Schema title"}); got != "Schema title" {
		t.Fatalf("GetTitleOrEmpty(explicit) = %q", got)
	}
	if got := GetDescriptionOrEmpty(nil); got != "" {
		t.Fatalf("GetDescriptionOrEmpty(nil) = %q", got)
	}
	if got := GetDescriptionOrEmpty(&FileOptions{Description: "Schema description"}); got != "Schema description" {
		t.Fatalf("GetDescriptionOrEmpty(explicit) = %q", got)
	}

	name := sourceName{source: sourceComments{
		detached: []string{" detached\n"},
		leading:  "leading\n",
		trailing: "trailing ",
	}}
	if got := GetDescriptionOrComment(name, &MessageOptions{Description: "Explicit description"}); got != "Explicit description" {
		t.Fatalf("explicit description = %q", got)
	}
	if got := GetDescriptionOrComment(name, nil); got != "detached\nleading\ntrailing" {
		t.Fatalf("source comments = %q", got)
	}
	if got := GetDescriptionOrComment(sourceName{}, &MessageOptions{}); got != "" {
		t.Fatalf("missing description and source = %q", got)
	}
}

func TestGetEntrypointMessagePrecedenceAndEmptyValues(t *testing.T) {
	tests := []struct {
		name   string
		plugin *PluginOptions
		file   *FileOptions
		want   string
	}{
		{name: "file overrides plugin", plugin: &PluginOptions{EntrypointMessage: "Plugin"}, file: &FileOptions{EntrypointMessage: "File"}, want: "File"},
		{name: "empty file falls back", plugin: &PluginOptions{EntrypointMessage: "Plugin"}, file: &FileOptions{}, want: "Plugin"},
		{name: "missing file falls back", plugin: &PluginOptions{EntrypointMessage: "Plugin"}, want: "Plugin"},
		{name: "nil options are empty", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := GetEntrypointMessage(test.plugin, test.file); got != test.want {
				t.Fatalf("GetEntrypointMessage() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestGetAdditionalPropertiesModes(t *testing.T) {
	trueValue, falseValue := true, false
	tests := []struct {
		name     string
		mode     PluginAdditionalProperties
		keywords *ObjectKeywords
		want     *bool
	}{
		{name: "always true ignores false", mode: PluginAdditionalProperties_AlwaysTrue, keywords: &ObjectKeywords{AdditionalProperties: &falseValue}, want: &trueValue},
		{name: "always false ignores true", mode: PluginAdditionalProperties_AlwaysFalse, keywords: &ObjectKeywords{AdditionalProperties: &trueValue}, want: &falseValue},
		{name: "default true with nil keywords", mode: PluginAdditionalProperties_DefaultTrue, want: &trueValue},
		{name: "default true with missing value", mode: PluginAdditionalProperties_DefaultTrue, keywords: &ObjectKeywords{}, want: &trueValue},
		{name: "default true preserves false", mode: PluginAdditionalProperties_DefaultTrue, keywords: &ObjectKeywords{AdditionalProperties: &falseValue}, want: &falseValue},
		{name: "default false with nil keywords", mode: PluginAdditionalProperties_DefaultFalse, want: &falseValue},
		{name: "default false with missing value", mode: PluginAdditionalProperties_DefaultFalse, keywords: &ObjectKeywords{}, want: &falseValue},
		{name: "default false preserves true", mode: PluginAdditionalProperties_DefaultFalse, keywords: &ObjectKeywords{AdditionalProperties: &trueValue}, want: &trueValue},
		{name: "do nothing with nil keywords", mode: PluginAdditionalProperties_DoNothing},
		{name: "do nothing with missing value", mode: PluginAdditionalProperties_DoNothing, keywords: &ObjectKeywords{}},
		{name: "do nothing preserves explicit value", mode: PluginAdditionalProperties_DoNothing, keywords: &ObjectKeywords{AdditionalProperties: &trueValue}, want: &trueValue},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := GetAdditionalProperties(&PluginOptions{AdditionalProperties: test.mode}, test.keywords)
			if test.want == nil {
				if got != nil {
					t.Fatalf("GetAdditionalProperties() = %v, want nil", *got)
				}
				return
			}
			if got == nil || *got != *test.want {
				t.Fatalf("GetAdditionalProperties() = %#v, want %v", got, *test.want)
			}
		})
	}
}

func TestGetAdditionalPropertiesRejectsUnsupportedMode(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("GetAdditionalProperties did not panic for unsupported mode")
		}
	}()
	GetAdditionalProperties(&PluginOptions{AdditionalProperties: PluginAdditionalProperties(99)}, nil)
}

type sourceName struct {
	source pgs.SourceCodeInfo
}

func (n sourceName) SourceCodeInfo() pgs.SourceCodeInfo { return n.source }
func (sourceName) Name() pgs.Name                       { return "fixture" }

type sourceComments struct {
	detached []string
	leading  string
	trailing string
}

func (s sourceComments) Location() *descriptorpb.SourceCodeInfo_Location { return nil }
func (s sourceComments) LeadingComments() string                         { return s.leading }
func (s sourceComments) LeadingDetachedComments() []string               { return s.detached }
func (s sourceComments) TrailingComments() string                        { return s.trailing }
