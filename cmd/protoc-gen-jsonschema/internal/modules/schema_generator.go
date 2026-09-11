package modules

import (
	"encoding/json"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/jsonschema"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/proto"
	pgs "github.com/lyft/protoc-gen-star/v2"
	"google.golang.org/protobuf/types/known/anypb"
)

func buildFromMessage(pluginOptions *proto.PluginOptions, message pgs.Message, mo *proto.MessageOptions) *jsonschema.Schema {
	schema := &jsonschema.Schema{}
	schema.Type = "object"
	schema.Title = proto.GetTitleOrEmpty(mo)
	schema.Description = proto.GetDescriptionOrComment(message, mo)
	schema.Properties = jsonschema.NewOrderedSchemaMap()

	fillSchemaByObjectKeywords(pluginOptions, schema, mo.GetObject())

	for _, field := range message.Fields() {
		propName := toPropertyName(field, pluginOptions.GetPreserveProtoFieldNames())
		fieldSchema := &jsonschema.Schema{Ref: toRefId(field)}
		if !pluginOptions.GetMandatoryNullable() && (field.InRealOneOf() || field.HasOptionalKeyword()) || proto.GetFieldOptions(field).GetNullable() {
			fieldSchema = &jsonschema.Schema{OneOf: []*jsonschema.Schema{
				{Type: "null"},
				fieldSchema,
			}}
		}

		if isRequiredField(pluginOptions, field) {
			// If field is not a member of oneOf
			schema.Required = append(schema.Required, propName)
		}

		schema.Properties.Set(propName, fieldSchema)
	}

	// 当前仅表达字段的 nullable/required，不生成 oneof 成员之间的互斥约束。
	return schema
}

func isRequiredField(pluginOptions *proto.PluginOptions, field pgs.Field) bool {
	if pluginOptions.GetRespectProtojsonPresence() {
		// To see the below link for more details
		// https://github.com/protocolbuffers/protobuf/blob/main/docs/implementing_proto3_presence.md#to-test-whether-a-field-should-have-presence
		return !field.InRealOneOf() && field.HasPresence()
	} else {
		return !field.InRealOneOf() && !field.HasOptionalKeyword() && !field.Type().IsRepeated() && !field.Type().IsMap()
	}
}

func buildFromEnum(enum pgs.Enum) (*jsonschema.Schema, error) {
	eo := proto.GetEnumOptions(enum)

	schema := &jsonschema.Schema{}
	switch eo.GetMappingType() {
	case proto.EnumOptions_MapToString:
		schema.Type = "string"
	case proto.EnumOptions_MapToNumber:
		schema.Type = "number"
	case proto.EnumOptions_MapToCustom:
		schema.Type = "string"
	}
	schema.Title = proto.GetTitleOrEmpty(eo)
	schema.Description = proto.GetDescriptionOrComment(enum, eo)

	for _, enumValue := range enum.Values() {
		switch eo.GetMappingType() {
		case proto.EnumOptions_MapToString:
			schema.Enum = append(schema.Enum, enumValue.Name().String())
		case proto.EnumOptions_MapToNumber:
			schema.Enum = append(schema.Enum, enumValue.Value())
		case proto.EnumOptions_MapToCustom:
			evo := proto.GetEnumValueOptions(enumValue)

			customValue, err := parseScalaValueFromAny(evo.GetCustomValue())
			if err != nil {
				return nil, err
			}

			if customValue == nil {
				schema.Enum = append(schema.Enum, enumValue.Name().String())
			} else {
				schema.Enum = append(schema.Enum, customValue)
			}
		}
	}
	return schema, nil
}

func parseScalaValueFromAny(anyValue *anypb.Any) (any, error) {
	if anyValue == nil || anyValue.Value == nil {
		return nil, nil
	}

	var value any
	if err := json.Unmarshal(anyValue.Value, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func toPropertyName(field pgs.Field, preserveProto bool) string {
	if !preserveProto && field.Descriptor().JsonName != nil {
		return field.Descriptor().GetJsonName()
	}
	return field.Name().String()
}

type FqdnResolver interface {
	FullyQualifiedName() string
}

func toRefId(resolver FqdnResolver) jsonschema.RefId {
	return jsonschema.RefId(resolver.FullyQualifiedName())
}
