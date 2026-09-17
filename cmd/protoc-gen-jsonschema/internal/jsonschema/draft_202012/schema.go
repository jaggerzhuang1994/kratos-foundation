package draft_202012

import (
	"fmt"

	"github.com/iancoleman/orderedmap"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/jsonschema"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/utils"
)

type Schema struct {
	// Version 标识输出采用的 JSON Schema 方言 URI。
	Version string `json:"$schema,omitempty"`
	// ID 保存 Schema 资源标识。
	ID string `json:"$id,omitempty"`
	// Anchor 保存供引用定位的锚点。
	Anchor string `json:"$anchor,omitempty"`
	// DynamicAnchor 保存动态引用的锚点名称。
	DynamicAnchor string `json:"$dynamicAnchor,omitempty"`
	// Ref 指向被复用的 Schema 定义。
	Ref string `json:"$ref,omitempty"`
	// DynamicRef 保存动态解析的 Schema 引用。
	DynamicRef string `json:"$dynamicRef,omitempty"`
	// Definitions 按稳定顺序保存可复用的子 Schema。
	Definitions *orderedmap.OrderedMap `json:"$defs,omitempty"`
	// Comments 保存面向维护者的 Schema 注释。
	Comments string `json:"$comment,omitempty"`

	// AllOf 要求同时满足所有子 Schema。
	AllOf []*Schema `json:"allOf,omitempty"`
	// AnyOf 要求至少满足一个子 Schema。
	AnyOf []*Schema `json:"anyOf,omitempty"`
	// OneOf 要求恰好满足一个子 Schema。
	OneOf []*Schema `json:"oneOf,omitempty"`
	// Not 指定不得满足的子 Schema。
	Not *Schema `json:"not,omitempty"`

	// If 保存条件判断 Schema。
	If *Schema `json:"if,omitempty"`
	// Then 在满足 If 时应用。
	Then *Schema `json:"then,omitempty"`
	// Else 在不满足 If 时应用。
	Else *Schema `json:"else,omitempty"`
	// DependentSchemas 按属性是否存在决定应用的对象 Schema。
	DependentSchemas *orderedmap.OrderedMap `json:"dependentSchemas,omitempty"`

	// PrefixItems 按位置约束数组前部元素。
	PrefixItems []*Schema `json:"prefixItems,omitempty"`
	// Items 约束 PrefixItems 之后的数组元素；未设 PrefixItems 时约束全部元素。
	Items *Schema `json:"items,omitempty"`
	// Contains 约束数组中需要匹配的元素。
	Contains *Schema `json:"contains,omitempty"`

	// Properties 按属性名保存字段 Schema。
	Properties *orderedmap.OrderedMap `json:"properties,omitempty"`
	// PatternProperties 按正则表达式匹配属性名并施加约束。
	PatternProperties *orderedmap.OrderedMap `json:"patternProperties,omitempty"`
	// AdditionalProperties 约束未被属性名或正则规则匹配的属性。
	AdditionalProperties *Schema `json:"additionalProperties,omitempty"`
	// PropertyNames 约束对象的属性名。
	PropertyNames *Schema `json:"propertyNames,omitempty"`

	// Type 保存 JSON 值类型。
	Type string `json:"type,omitempty"`
	// Enum 列出允许的取值。
	Enum []any `json:"enum,omitempty"`
	// Const 指定唯一允许值；nil 表示未设置此约束。
	Const *any `json:"const,omitempty"`
	// MultipleOf 为数值倍数约束；nil 表示未设置。
	MultipleOf *int `json:"multipleOf,omitempty"`
	// Maximum 为含边界的数值上限；nil 表示未设置。
	Maximum *float64 `json:"maximum,omitempty"`
	// ExclusiveMaximum 为不含边界的数值上限；nil 表示未设置。
	ExclusiveMaximum *float64 `json:"exclusiveMaximum,omitempty"`
	// Minimum 为含边界的数值下限；nil 表示未设置。
	Minimum *float64 `json:"minimum,omitempty"`
	// ExclusiveMinimum 为不含边界的数值下限；nil 表示未设置。
	ExclusiveMinimum *float64 `json:"exclusiveMinimum,omitempty"`
	// MaxLength 为字符串最大字符数；nil 表示未设置。
	MaxLength *int `json:"maxLength,omitempty"`
	// MinLength 为字符串最小字符数；nil 表示未设置。
	MinLength *int `json:"minLength,omitempty"`
	// Pattern 为字符串正则表达式约束。
	Pattern string `json:"pattern,omitempty"`
	// MaxItems 为数组元素数上限；nil 表示未设置。
	MaxItems *int `json:"maxItems,omitempty"`
	// MinItems 为数组元素数下限；nil 表示未设置。
	MinItems *int `json:"minItems,omitempty"`
	// UniqueItems 表示数组元素是否必须唯一；nil 表示未设置。
	UniqueItems *bool `json:"uniqueItems,omitempty"`
	// MaxContains 为匹配 Contains 的元素数上限；nil 表示未设置。
	MaxContains *int `json:"maxContains,omitempty"`
	// MinContains 为匹配 Contains 的元素数下限；nil 表示未设置。
	MinContains *int `json:"minContains,omitempty"`
	// MaxProperties 为对象属性数上限；nil 表示未设置。
	MaxProperties *int `json:"maxProperties,omitempty"`
	// MinProperties 为对象属性数下限；nil 表示未设置。
	MinProperties *int `json:"minProperties,omitempty"`
	// Required 列出对象必须包含的属性名。
	Required []string `json:"required,omitempty"`
	// DependentRequired 保存某属性存在时必须同时出现的其他属性。
	DependentRequired map[string][]string `json:"dependentRequired,omitempty"`

	// Format 保存日期、地址等语义格式提示。
	Format string `json:"format,omitempty"`

	// ContentEncoding 描述字符串内容的编码方式。
	ContentEncoding string `json:"contentEncoding,omitempty"`
	// ContentMediaType 描述解码后内容的媒体类型。
	ContentMediaType string `json:"contentMediaType,omitempty"`
	// ContentSchema 描述解码后内容的 Schema。
	ContentSchema *Schema `json:"contentSchema,omitempty"`

	// Title 为面向使用者的简短标题。
	Title string `json:"title,omitempty"`
	// Description 为面向使用者的说明文字。
	Description string `json:"description,omitempty"`
	// Default 保存默认值注解；不会在生成阶段替业务配置填充值。
	Default *any `json:"default,omitempty"`
	// Deprecated 标记是否弃用；nil 表示未设置。
	Deprecated *bool `json:"deprecated,omitempty"`
	// ReadOnly 标记只读语义；nil 表示未设置。
	ReadOnly *bool `json:"readOnly,omitempty"`
	// WriteOnly 标记只写语义；nil 表示未设置。
	WriteOnly *bool `json:"writeOnly,omitempty"`
	// Examples 保存说明用的示例值。
	Examples []any `json:"examples,omitempty"`

	// Extras 复制生成过程的辅助标记；json:"-" 使其不进入 JSON/YAML 输出。
	Extras map[string]any `json:"-"`
}

func New(schema *jsonschema.Schema) *Schema {
	return deepCopy(schema)
}

func (s *Schema) Schema() string {
	return s.Version
}

func (s *Schema) RefID() string {
	return s.Ref
}

func (s *Schema) Merge(draft jsonschema.Draft) error {
	draft2, ok := draft.(*Schema)
	if !ok {
		return fmt.Errorf("draft is not a draft_202012.Schema")
	}
	if s.Definitions == nil {
		s.Definitions = orderedmap.New()
	}
	// 把 draft -> 合并到 s 中
	// 把 draft.defs 合并到 s.defs 中 (如果有重复，则不覆盖)
	if draft2.Definitions != nil {
		for _, key := range draft2.Definitions.Keys() {
			if _, ok := s.Definitions.Get(key); !ok {
				value, _ := draft2.Definitions.Get(key)
				s.Definitions.Set(key, value)
			}
		}
	}
	var mergeRefID string
	var i = 0
	for {
		mergeRefID = fmt.Sprintf(".Merge_%d", i)
		if _, ok := s.Definitions.Get(mergeRefID); !ok {
			break
		}
		i++
	}
	mergeSchema := &Schema{}
	mergeSchema.AllOf = []*Schema{
		{
			Ref: s.Ref,
		},
		{
			Ref: draft2.Ref,
		},
	}
	s.Definitions.Set(mergeRefID, mergeSchema)
	s.Ref = "#/$defs/" + mergeRefID
	return nil
}

func deepCopy(origin *jsonschema.Schema) *Schema {
	if origin == nil {
		return nil
	}

	dst := &Schema{}
	dst.Version = origin.Version
	dst.ID = origin.ID
	dst.Anchor = origin.Anchor
	dst.DynamicAnchor = origin.DynamicAnchor
	if origin.Ref.String() != "" {
		dst.Ref = "#/$defs/" + origin.Ref.String()
	}
	dst.DynamicRef = origin.DynamicRef
	dst.Definitions = deepCopyMap(origin.Definitions)
	dst.Comments = origin.Comments

	dst.AllOf = deepCopyArray(origin.AllOf)
	dst.AnyOf = deepCopyArray(origin.AnyOf)
	dst.OneOf = deepCopyArray(origin.OneOf)
	dst.Not = deepCopy(origin.Not)

	dst.If = deepCopy(origin.If)
	dst.Then = deepCopy(origin.Then)
	dst.Else = deepCopy(origin.Else)
	dst.DependentSchemas = deepCopyMap(origin.DependentSchemas)

	dst.PrefixItems = deepCopyArray(origin.PrefixItems)
	dst.Items = deepCopy(origin.Items)
	dst.Contains = deepCopy(origin.Contains)

	dst.Properties = deepCopyMap(origin.Properties)
	dst.PatternProperties = deepCopyMap(origin.PatternProperties)
	dst.AdditionalProperties = deepCopy(origin.AdditionalProperties)
	dst.PropertyNames = deepCopy(origin.PropertyNames)

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
	dst.ContentSchema = deepCopy(origin.ContentSchema)

	dst.Title = origin.Title
	dst.Description = origin.Description
	dst.Default = utils.CopyAnyP(origin.Default)
	dst.Deprecated = utils.CopyBoolP(origin.Deprecated)
	dst.ReadOnly = utils.CopyBoolP(origin.ReadOnly)
	dst.WriteOnly = utils.CopyBoolP(origin.WriteOnly)
	dst.Examples = utils.CopyAnyArray(origin.Examples)

	dst.Extras = utils.CopyMapAny(origin.Extras)

	// Do nothing if IsBooleanSchema is true. Cause empty schema means true
	// If IsBooleanSchema is false, { not: {} } is generated
	if origin.IsBooleanSchema != nil && !*origin.IsBooleanSchema {
		dst.Not = &Schema{}
	}
	return dst
}

func deepCopyArray(arr []*jsonschema.Schema) []*Schema {
	dst := make([]*Schema, len(arr))
	for i, schema := range arr {
		dst[i] = deepCopy(schema)
	}
	return dst
}

func deepCopyMap(schemaMap jsonschema.SchemaMap) *orderedmap.OrderedMap {
	if schemaMap == nil {
		return nil
	}
	dst := orderedmap.New()
	for _, key := range schemaMap.Keys() {
		schema, _ := schemaMap.Get(key)
		dst.Set(key, deepCopy(schema))
	}
	return dst
}
