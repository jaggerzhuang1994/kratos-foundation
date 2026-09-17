package jsonschema

import (
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/utils"
)

type Draft interface {
	Schema() string
	RefID() string
	Merge(draft Draft) error
}

type Schema struct {
	// Version 标识输出采用的 JSON Schema 方言 URI。
	Version string
	// ID 保存 Schema 资源标识。
	ID string
	// Anchor 保存供引用定位的锚点。
	Anchor string
	// DynamicAnchor 保存动态引用的锚点名称。
	DynamicAnchor string
	// Ref 保存中间 Schema 的逻辑引用名，输出时转换为对应方言的引用路径。
	Ref RefId
	// DynamicRef 保存动态解析的 Schema 引用。
	DynamicRef string
	// Definitions 按稳定顺序保存可复用的子 Schema。
	Definitions SchemaMap
	// Comments 保存面向维护者的 Schema 注释。
	Comments string

	// AllOf 要求同时满足所有子 Schema。
	AllOf []*Schema
	// AnyOf 要求至少满足一个子 Schema。
	AnyOf []*Schema
	// OneOf 要求恰好满足一个子 Schema。
	OneOf []*Schema
	// Not 指定不得满足的子 Schema。
	Not *Schema

	// If 保存条件判断 Schema。
	If *Schema
	// Then 在满足 If 时应用。
	Then *Schema
	// Else 在不满足 If 时应用。
	Else *Schema
	// DependentSchemas 按属性是否存在决定应用的对象 Schema。
	DependentSchemas SchemaMap

	// PrefixItems 按位置约束数组前部元素。
	PrefixItems []*Schema
	// Items 保存数组元素约束。
	Items *Schema
	// Contains 约束数组中需要匹配的元素。
	Contains *Schema

	// Properties 按属性名保存字段 Schema。
	Properties SchemaMap
	// PatternProperties 按正则表达式匹配属性名并施加约束。
	PatternProperties SchemaMap
	// AdditionalProperties 约束未被属性名或正则规则匹配的属性。
	AdditionalProperties *Schema
	// PropertyNames 约束对象的属性名。
	PropertyNames *Schema

	// Type 保存 JSON 值类型。
	Type string
	// Enum 列出允许的取值。
	Enum []any
	// Const 指定唯一允许值；nil 表示未设置此约束。
	Const *any
	// MultipleOf 为数值倍数约束；nil 表示未设置。
	MultipleOf *int
	// Maximum 为含边界的数值上限；nil 表示未设置。
	Maximum *float64
	// ExclusiveMaximum 为不含边界的数值上限；nil 表示未设置。
	ExclusiveMaximum *float64
	// Minimum 为含边界的数值下限；nil 表示未设置。
	Minimum *float64
	// ExclusiveMinimum 为不含边界的数值下限；nil 表示未设置。
	ExclusiveMinimum *float64
	// MaxLength 为字符串最大字符数；nil 表示未设置。
	MaxLength *int
	// MinLength 为字符串最小字符数；nil 表示未设置。
	MinLength *int
	// Pattern 为字符串正则表达式约束。
	Pattern string
	// MaxItems 为数组元素数上限；nil 表示未设置。
	MaxItems *int
	// MinItems 为数组元素数下限；nil 表示未设置。
	MinItems *int
	// UniqueItems 表示数组元素是否必须唯一；nil 表示未设置。
	UniqueItems *bool
	// MaxContains 为匹配 Contains 的元素数上限；nil 表示未设置。
	MaxContains *int
	// MinContains 为匹配 Contains 的元素数下限；nil 表示未设置。
	MinContains *int
	// MaxProperties 为对象属性数上限；nil 表示未设置。
	MaxProperties *int
	// MinProperties 为对象属性数下限；nil 表示未设置。
	MinProperties *int
	// Required 列出对象必须包含的属性名。
	Required []string
	// DependentRequired 保存某属性存在时必须同时出现的其他属性。
	DependentRequired map[string][]string

	// Format 保存日期、地址等语义格式提示。
	Format string

	// ContentEncoding 描述字符串内容的编码方式。
	ContentEncoding string
	// ContentMediaType 描述解码后内容的媒体类型。
	ContentMediaType string
	// ContentSchema 描述解码后内容的 Schema。
	ContentSchema *Schema

	// Title 为面向使用者的简短标题。
	Title string
	// Description 为面向使用者的说明文字。
	Description string
	// Default 保存默认值注解；不会在生成阶段替业务配置填充值。
	Default *any
	// Deprecated 标记是否弃用；nil 表示未设置。
	Deprecated *bool
	// ReadOnly 标记只读语义；nil 表示未设置。
	ReadOnly *bool
	// WriteOnly 标记只写语义；nil 表示未设置。
	WriteOnly *bool
	// Examples 保存说明用的示例值。
	Examples []any

	// Extras 保存入口可达性等生成过程辅助标记，方言转换后不参与序列化。
	Extras map[string]any

	// IsBooleanSchema 为 nil 时使用普通 Schema；false 转换为 not:{}，true 不额外添加约束。
	IsBooleanSchema *bool
}

func (s *Schema) SetExtrasItem(key string, value any) {
	if s.Extras == nil {
		s.Extras = map[string]any{}
	}
	s.Extras[key] = value
}

func (s *Schema) ClearExtras() {
	s.Extras = nil
}

func (s *Schema) GetExtrasItem(key string) any {
	if s.Extras == nil {
		return nil
	}
	return s.Extras[key]
}

func DeepCopy(origin *Schema) *Schema {
	if origin == nil {
		return nil
	}

	dst := &Schema{}
	dst.Version = origin.Version
	dst.ID = origin.ID
	dst.Anchor = origin.Anchor
	dst.DynamicAnchor = origin.DynamicAnchor
	dst.Ref = origin.Ref
	dst.DynamicRef = origin.DynamicRef
	dst.Definitions = DeepCopyMap(origin.Definitions)
	dst.Comments = origin.Comments

	dst.AllOf = DeepCopyArray(origin.AllOf)
	dst.AnyOf = DeepCopyArray(origin.AnyOf)
	dst.OneOf = DeepCopyArray(origin.OneOf)
	dst.Not = DeepCopy(origin.Not)

	dst.If = DeepCopy(origin.If)
	dst.Then = DeepCopy(origin.Then)
	dst.Else = DeepCopy(origin.Else)
	dst.DependentSchemas = DeepCopyMap(origin.DependentSchemas)

	dst.PrefixItems = DeepCopyArray(origin.PrefixItems)
	dst.Items = DeepCopy(origin.Items)
	dst.Contains = DeepCopy(origin.Contains)

	dst.Properties = DeepCopyMap(origin.Properties)
	dst.PatternProperties = DeepCopyMap(origin.PatternProperties)
	dst.AdditionalProperties = DeepCopy(origin.AdditionalProperties)
	dst.PropertyNames = DeepCopy(origin.PropertyNames)

	dst.Type = origin.Type
	dst.Enum = utils.CopyAnyArray(origin.Enum)
	dst.Const = utils.CopyAnyP(origin.Const)
	dst.MultipleOf = utils.CopyIntP(origin.MultipleOf)
	dst.Maximum = utils.CopyFloat64P(origin.Maximum)
	dst.ExclusiveMaximum = utils.CopyFloat64P(origin.ExclusiveMaximum)
	dst.Minimum = utils.CopyFloat64P(origin.Minimum)
	dst.ExclusiveMinimum = utils.CopyFloat64P(origin.ExclusiveMinimum)
	dst.MaxLength = utils.CopyIntP(origin.MaxLength)
	dst.MinLength = utils.CopyIntP(origin.MinLength)
	dst.Pattern = origin.Pattern
	dst.MaxItems = utils.CopyIntP(origin.MaxItems)
	dst.MinItems = utils.CopyIntP(origin.MinItems)
	dst.UniqueItems = utils.CopyBoolP(origin.UniqueItems)
	dst.MaxContains = utils.CopyIntP(origin.MaxContains)
	dst.MinContains = utils.CopyIntP(origin.MinContains)
	dst.MaxProperties = utils.CopyIntP(origin.MaxProperties)
	dst.MinProperties = utils.CopyIntP(origin.MinProperties)
	dst.Required = utils.CopyStringArray(origin.Required)
	dst.DependentRequired = utils.CopyMapStringArray(origin.DependentRequired)

	dst.Format = origin.Format

	dst.ContentEncoding = origin.ContentEncoding
	dst.ContentMediaType = origin.ContentMediaType
	dst.ContentSchema = DeepCopy(origin.ContentSchema)

	dst.Title = origin.Title
	dst.Description = origin.Description
	dst.Default = utils.CopyAnyP(origin.Default)
	dst.Deprecated = utils.CopyBoolP(origin.Deprecated)
	dst.ReadOnly = utils.CopyBoolP(origin.ReadOnly)
	dst.WriteOnly = utils.CopyBoolP(origin.WriteOnly)
	dst.Examples = utils.CopyAnyArray(origin.Examples)

	dst.Extras = utils.CopyMapAny(origin.Extras)

	dst.IsBooleanSchema = utils.CopyBoolP(origin.IsBooleanSchema)
	return dst
}

func DeepCopyArray(schemas []*Schema) []*Schema {
	if schemas == nil {
		return nil
	}

	newSchemas := make([]*Schema, len(schemas))
	for i, schema := range schemas {
		newSchemas[i] = DeepCopy(schema)
	}
	return newSchemas
}

func DeepCopyMap(schemas SchemaMap) SchemaMap {
	if schemas == nil {
		return nil
	}

	newSchemas := NewOrderedSchemaMap()
	for _, key := range schemas.Keys() {
		schema, _ := schemas.Get(key)
		newSchemas.Set(key, DeepCopy(schema))
	}
	return newSchemas
}

// Boolean Schema is special type of schema
var (
	// TrueSchema equals to `{}` or `true`
	TrueSchema = *NewBooleanSchema(true)
	// FalseSchema equals to `{"not": {}}` or `false`
	FalseSchema = *NewBooleanSchema(false)
)

func NewBooleanSchema(b bool) *Schema {
	return &Schema{IsBooleanSchema: &b}
}

type RefId string

func (i RefId) String() string {
	return string(i)
}

func (i RefId) IsEmpty() bool {
	return i.String() == ""
}
