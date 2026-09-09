package modules

import (
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/jsonschema"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/proto"
	pgs "github.com/lyft/protoc-gen-star/v2"
	"slices"
)

func buildFromWellKnownMessage(pluginOptions *proto.PluginOptions, message pgs.Message, mo *proto.MessageOptions) *jsonschema.Schema {
	baseSchema := buildFromMessage(pluginOptions, message, mo)

	wellKnownType := getWellKnownMessageType(message)
	if wellKnownType == WellKnownMessageTypeNone {
		panic("not well known type")
	}
	switch wellKnownType {
	case WellKnownMessageTypeK8sIntOrString:
		schema := &jsonschema.Schema{}
		schema.Title = baseSchema.Title
		schema.Description = baseSchema.Description
		schema.OneOf = []*jsonschema.Schema{
			{Type: "string"},
			{Type: "integer"},
		}
		return schema
	case WellKnownMessageTypeK8sVolume:
		baseSchema.OneOf = []*jsonschema.Schema{{Ref: ".k8s.io.api.core.v1.VolumeSource"}}
		baseSchema.Required = deletePropertyInRequired(baseSchema.Required, "volumeSource")
		baseSchema.Properties.Delete("volumeSource")
	case WellKnownMessageTypeK8sSecretProjection:
		baseSchema.OneOf = []*jsonschema.Schema{{Ref: ".k8s.io.api.core.v1.LocalObjectReference"}}
		baseSchema.Required = deletePropertyInRequired(baseSchema.Required, "localObjectReference")
		baseSchema.Properties.Delete("localObjectReference")
	case WellKnownMessageTypeK8sConfigMapVolumeSource:
		baseSchema.OneOf = []*jsonschema.Schema{{Ref: ".k8s.io.api.core.v1.LocalObjectReference"}}
		baseSchema.Required = deletePropertyInRequired(baseSchema.Required, "localObjectReference")
		baseSchema.Properties.Delete("localObjectReference")
	case WellKnownMessageTypeK8sConfigMapProjection:
		baseSchema.OneOf = []*jsonschema.Schema{{Ref: ".k8s.io.api.core.v1.LocalObjectReference"}}
		baseSchema.Required = deletePropertyInRequired(baseSchema.Required, "localObjectReference")
		baseSchema.Properties.Delete("localObjectReference")
	case WellKnownMessageTypeK8sConfigMapKeySelector:
		baseSchema.OneOf = []*jsonschema.Schema{{Ref: ".k8s.io.api.core.v1.LocalObjectReference"}}
		baseSchema.Required = deletePropertyInRequired(baseSchema.Required, "localObjectReference")
		baseSchema.Properties.Delete("localObjectReference")
	case WellKnownMessageTypeK8sSecretKeySelector:
		baseSchema.OneOf = []*jsonschema.Schema{{Ref: ".k8s.io.api.core.v1.LocalObjectReference"}}
		baseSchema.Required = deletePropertyInRequired(baseSchema.Required, "localObjectReference")
		baseSchema.Properties.Delete("localObjectReference")
	case WellKnownMessageTypeK8sConfigMapEnvSource:
		baseSchema.OneOf = []*jsonschema.Schema{{Ref: ".k8s.io.api.core.v1.LocalObjectReference"}}
		baseSchema.Required = deletePropertyInRequired(baseSchema.Required, "localObjectReference")
		baseSchema.Properties.Delete("localObjectReference")
	case WellKnownMessageTypeK8sSecretEnvSource:
		baseSchema.OneOf = []*jsonschema.Schema{{Ref: ".k8s.io.api.core.v1.LocalObjectReference"}}
		baseSchema.Required = deletePropertyInRequired(baseSchema.Required, "localObjectReference")
		baseSchema.Properties.Delete("localObjectReference")
	case WellKnownMessageTypeK8sProbe:
		baseSchema.OneOf = []*jsonschema.Schema{{Ref: ".k8s.io.api.core.v1.ProbeHandler"}}
		baseSchema.Required = deletePropertyInRequired(baseSchema.Required, "handler")
		baseSchema.Properties.Delete("handler")
	case WellKnownMessageTypeK8sEphemeralContainer:
		baseSchema.OneOf = []*jsonschema.Schema{{Ref: ".k8s.io.api.core.v1.EphemeralContainerCommon"}}
		baseSchema.Required = deletePropertyInRequired(baseSchema.Required, "ephemeralContainerCommon")
		baseSchema.Properties.Delete("ephemeralContainerCommon")
	}
	return baseSchema
}

type WellKnownMessageType int

const (
	WellKnownMessageTypeNone WellKnownMessageType = iota
	WellKnownMessageTypeK8sIntOrString
	WellKnownMessageTypeK8sVolume
	WellKnownMessageTypeK8sSecretProjection
	WellKnownMessageTypeK8sConfigMapVolumeSource
	WellKnownMessageTypeK8sConfigMapProjection
	WellKnownMessageTypeK8sConfigMapKeySelector
	WellKnownMessageTypeK8sSecretKeySelector
	WellKnownMessageTypeK8sConfigMapEnvSource
	WellKnownMessageTypeK8sSecretEnvSource
	WellKnownMessageTypeK8sProbe
	WellKnownMessageTypeK8sEphemeralContainer
)

func isWellKnownMessage(message pgs.Message) bool {
	return getWellKnownMessageType(message) != WellKnownMessageTypeNone
}

func getWellKnownMessageType(message pgs.Message) WellKnownMessageType {
	switch message.FullyQualifiedName() {
	case ".k8s.io.apimachinery.pkg.util.intstr.IntOrString":
		return WellKnownMessageTypeK8sIntOrString
	case ".k8s.io.api.core.v1.Volume":
		return WellKnownMessageTypeK8sVolume
	case ".k8s.io.api.core.v1.SecretProjection":
		return WellKnownMessageTypeK8sSecretProjection
	case ".k8s.io.api.core.v1.ConfigMapVolumeSource":
		return WellKnownMessageTypeK8sConfigMapVolumeSource
	case ".k8s.io.api.core.v1.ConfigMapProjection":
		return WellKnownMessageTypeK8sConfigMapProjection
	case ".k8s.io.api.core.v1.ConfigMapKeySelector":
		return WellKnownMessageTypeK8sConfigMapKeySelector
	case ".k8s.io.api.core.v1.SecretKeySelector":
		return WellKnownMessageTypeK8sSecretKeySelector
	case ".k8s.io.api.core.v1.ConfigMapEnvSource":
		return WellKnownMessageTypeK8sConfigMapEnvSource
	case ".k8s.io.api.core.v1.SecretEnvSource":
		return WellKnownMessageTypeK8sSecretEnvSource
	case ".k8s.io.api.core.v1.Probe":
		return WellKnownMessageTypeK8sProbe
	case ".k8s.io.api.core.v1.EphemeralContainer":
		return WellKnownMessageTypeK8sEphemeralContainer
	}

	return WellKnownMessageTypeNone
}

func buildFromWellKnownField(field pgs.Field, fo *proto.FieldOptions) *jsonschema.Schema {
	schema := &jsonschema.Schema{}
	schema.Title = proto.GetTitleOrEmpty(fo)
	schema.Description = proto.GetDescriptionOrComment(field, fo)

	wellKnownType := getWellKnownFieldType(field)
	if wellKnownType == WellKnownTypeNone {
		panic("not well known type")
	}

	fillWellKnownSchema(schema, wellKnownType, fo)

	if field.Type().IsRepeated() {
		return wrapSchemaInArray(schema, field, fo)
	} else {
		return schema
	}
}

func isWellKnownField(field pgs.Field) bool {
	return getWellKnownFieldType(field) != WellKnownTypeNone
}

type WellKnownFieldType int

const (
	WellKnownTypeNone WellKnownFieldType = iota
	WellKnownTypeTimestamp
	WellKnownTypeDuration
	WellKnownTypeAny
	WellKnownTypeNullValue
)

func getWellKnownFieldType(field pgs.Field) WellKnownFieldType {
	if field.Type().IsMap() {
		return WellKnownTypeNone
	}

	if field.Type().ProtoType() == pgs.EnumT {
		var enum pgs.Enum
		if field.Type().IsRepeated() {
			enum = field.Type().Element().Enum()
		} else {
			enum = field.Type().Enum()
		}
		if enum.FullyQualifiedName() == ".google.protobuf.NullValue" {
			return WellKnownTypeNullValue
		}
		return WellKnownTypeNone
	}
	if field.Type().ProtoType() != pgs.MessageT {
		return WellKnownTypeNone
	}
	var message pgs.Message
	if field.Type().IsRepeated() {
		message = field.Type().Element().Embed()
	} else {
		message = field.Type().Embed()
	}

	return wellKnownFieldType(message.FullyQualifiedName())
}

func wellKnownFieldType(name string) WellKnownFieldType {
	switch name {
	case ".google.protobuf.Timestamp":
		return WellKnownTypeTimestamp
	case ".google.protobuf.Duration":
		return WellKnownTypeDuration
	case ".google.protobuf.Any":
		return WellKnownTypeAny
	case ".google.protobuf.NullValue":
		return WellKnownTypeNullValue
	default:
		return WellKnownTypeNone
	}
}

func deletePropertyInRequired(required []string, item string) []string {
	return slices.DeleteFunc(required, func(val string) bool {
		return val == item
	})
}

// fillWellKnownSchema 让普通字段、数组元素和 map 值共用 ProtoJSON 类型映射。
func fillWellKnownSchema(schema *jsonschema.Schema, wellKnownType WellKnownFieldType, fo *proto.FieldOptions) {
	switch wellKnownType {
	case WellKnownTypeTimestamp:
		schema.Type = "string"
		schema.Format = "date-time"
		fillSchemaByStringKeywords(schema, fo.GetString_())
	case WellKnownTypeDuration:
		schema.Type = "string"
		// ProtoJSON 使用带符号的秒数字符串，最多保留纳秒精度，不使用 duration format。
		schema.Pattern = `^-?[0-9]+(\.[0-9]{1,9})?s$`
		fillSchemaByStringKeywords(schema, fo.GetString_())
	case WellKnownTypeAny:
		schema.Type = "object"
	case WellKnownTypeNullValue:
		schema.Type = "null"
	}
}
