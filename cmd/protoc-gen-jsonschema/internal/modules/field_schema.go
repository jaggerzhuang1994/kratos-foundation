package modules

import (
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/jsonschema"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/proto"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/utils"
	pgs "github.com/lyft/protoc-gen-star/v2"
)

func buildFromMessageField(field pgs.Field, fo *proto.FieldOptions) *jsonschema.Schema {
	schema := &jsonschema.Schema{}
	schema.Title = proto.GetTitleOrEmpty(fo)
	schema.Description = proto.GetDescriptionOrComment(field, fo)

	if field.Type().IsRepeated() {
		schema.Ref = toRefId(field.Type().Element().Embed())
	} else {
		schema.Ref = toRefId(field.Type().Embed())
	}

	if field.Type().IsRepeated() {
		return wrapSchemaInArray(schema, field, fo)
	} else {
		return schema
	}
}

func buildFromMapField(pluginOptions *proto.PluginOptions, field pgs.Field, fo *proto.FieldOptions) *jsonschema.Schema {
	schema := &jsonschema.Schema{}
	schema.Title = proto.GetTitleOrEmpty(fo)
	schema.Description = proto.GetDescriptionOrComment(field, fo)
	schema.Type = "object"

	valueSchema := &jsonschema.Schema{}
	value := field.Type().Element()
	protoType := value.ProtoType()
	if protoType.IsInt() {
		if pluginOptions.GetRespectProtojsonInt64() && isInt64(protoType) {
			valueSchema.Type = "string"
			valueSchema.Format = "int64"
		} else {
			valueSchema.Type = "integer"
			fillSchemaByNumericKeywords(valueSchema, fo.GetNumeric())
		}
	} else if protoType.IsNumeric() {
		valueSchema.Type = "number"
	} else if protoType == pgs.MessageT {
		if known := wellKnownFieldType(value.Embed().FullyQualifiedName()); known != WellKnownTypeNone {
			fillWellKnownSchema(valueSchema, known, fo)
		} else {
			valueSchema.Ref = toRefId(value.Embed())
		}
	} else if protoType == pgs.BoolT {
		valueSchema.Type = "boolean"
	} else if protoType == pgs.EnumT {
		if known := wellKnownFieldType(value.Enum().FullyQualifiedName()); known != WellKnownTypeNone {
			fillWellKnownSchema(valueSchema, known, fo)
		} else {
			valueSchema.Ref = toRefId(value.Enum())
		}
	} else if protoType == pgs.StringT {
		valueSchema.Type = "string"
	} else if protoType == pgs.BytesT {
		valueSchema.Type = "string"
		// Base64 Regex Expression
		valueSchema.Pattern = "^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$"
	}
	schema.AdditionalProperties = valueSchema
	return schema
}

func buildFromScalaField(pluginOptions *proto.PluginOptions, field pgs.Field, fo *proto.FieldOptions) *jsonschema.Schema {
	schema := &jsonschema.Schema{}
	schema.Title = proto.GetTitleOrEmpty(fo)
	schema.Description = proto.GetDescriptionOrComment(field, fo)

	protoType := field.Type().ProtoType()
	if protoType.IsInt() {
		if pluginOptions.GetRespectProtojsonInt64() && isInt64(protoType) {
			schema.Type = "string"
			schema.Format = "int64"
		} else {
			schema.Type = "integer"
			fillSchemaByNumericKeywords(schema, fo.GetNumeric())
		}
	} else if protoType == pgs.DoubleT || protoType == pgs.FloatT {
		schema.Type = "number"
		fillSchemaByNumericKeywords(schema, fo.GetNumeric())
	} else if protoType == pgs.BoolT {
		schema.Type = "boolean"
	} else if protoType == pgs.EnumT {
		if field.Type().IsRepeated() {
			schema.Ref = toRefId(field.Type().Element().Enum())
		} else {
			schema.Ref = toRefId(field.Type().Enum())
		}
	} else if protoType == pgs.StringT {
		schema.Type = "string"
		fillSchemaByStringKeywords(schema, fo.GetString_())
	} else if protoType == pgs.BytesT {
		schema.Type = "string"
		// Base64 Regex Expression
		schema.Pattern = "^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$"
		fillSchemaByStringKeywords(schema, fo.GetString_())
	}

	if field.Type().IsRepeated() {
		return wrapSchemaInArray(schema, field, fo)
	} else {
		return schema
	}
}

func wrapSchemaInArray(schema *jsonschema.Schema, field pgs.Field, fo *proto.FieldOptions) *jsonschema.Schema {
	repeatedSchema := &jsonschema.Schema{}
	repeatedSchema.Title = schema.Title
	repeatedSchema.Description = schema.Description
	repeatedSchema.Type = "array"
	repeatedSchema.Items = schema

	fillSchemaByArrayKeywords(repeatedSchema, fo.GetArray())
	return repeatedSchema
}

func isScalarType(field pgs.Field) bool {
	protoType := field.Type().ProtoType()
	if protoType.IsNumeric() {
		return true
	}
	if protoType == pgs.BoolT {
		return true
	}
	if protoType == pgs.EnumT {
		return true
	}
	if protoType == pgs.StringT || protoType == pgs.BytesT {
		return true
	}
	return false
}

func isInt64(protoType pgs.ProtoType) bool {
	switch protoType {
	case pgs.Int64T, pgs.UInt64T, pgs.SFixed64, pgs.SInt64, pgs.Fixed64T:
		return true
	}
	return false
}

func fillSchemaByObjectKeywords(pluginOptions *proto.PluginOptions, schema *jsonschema.Schema, keywords *proto.ObjectKeywords) {
	if additionalProperties := proto.GetAdditionalProperties(pluginOptions, keywords); additionalProperties != nil {
		schema.AdditionalProperties = jsonschema.NewBooleanSchema(*additionalProperties)
	}

	if keywords == nil {
		return
	}
	if keywords.MinProperties != nil {
		schema.MinProperties = utils.UInt32(keywords.GetMinProperties())
	}
	if keywords.MaxProperties != nil {
		schema.MaxProperties = utils.UInt32(keywords.GetMaxProperties())
	}
}

func fillSchemaByNumericKeywords(schema *jsonschema.Schema, keywords *proto.NumericKeywords) {
	if keywords == nil {
		return
	}

	if val, ok := keywords.Min.(*proto.NumericKeywords_InclusiveMinimum); ok {
		schema.Minimum = &val.InclusiveMinimum
	}
	if val, ok := keywords.Max.(*proto.NumericKeywords_InclusiveMaximum); ok {
		schema.Maximum = &val.InclusiveMaximum
	}
	if val, ok := keywords.Max.(*proto.NumericKeywords_ExclusiveMaximum); ok {
		schema.ExclusiveMaximum = &val.ExclusiveMaximum
	}
	if val, ok := keywords.Min.(*proto.NumericKeywords_ExclusiveMinimum); ok {
		schema.ExclusiveMinimum = &val.ExclusiveMinimum
	}

	if keywords.MultipleOf != nil {
		schema.MultipleOf = utils.Int32(keywords.GetMultipleOf())
	}
}

func fillSchemaByStringKeywords(schema *jsonschema.Schema, keywords *proto.StringKeywords) {
	if keywords == nil {
		return
	}

	if keywords.Pattern != "" {
		schema.Pattern = keywords.GetPattern()
	}
	if keywords.Format != "" {
		schema.Format = keywords.GetFormat()
	}

	if keywords.MaxLength != nil {
		schema.MaxLength = utils.UInt32(keywords.GetMaxLength())
	}
	if keywords.MinLength != nil {
		schema.MinLength = utils.UInt32(keywords.GetMinLength())
	}
	if keywords.Enum != nil {
		for _, e := range keywords.Enum {
			schema.Enum = append(schema.Enum, e)
		}
	}
}

func fillSchemaByArrayKeywords(schema *jsonschema.Schema, keywords *proto.ArrayKeywords) {
	if keywords == nil {
		return
	}

	if keywords.MaxItems != nil {
		schema.MaxItems = utils.UInt32(keywords.GetMaxItems())
	}
	if keywords.MinItems != nil {
		schema.MinItems = utils.UInt32(keywords.GetMinItems())
	}
	if keywords.UniqueItems != nil {
		schema.UniqueItems = keywords.UniqueItems
	}
}
