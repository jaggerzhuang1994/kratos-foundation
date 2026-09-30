package modules

import (
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/jsonschema"
	pgs "github.com/lyft/protoc-gen-star/v2"
)

// OptimizerImpl 只遍历调用方提供的 Registry，不保存跨次生成状态。
type OptimizerImpl struct{}

func NewOptimizerImpl() *OptimizerImpl {
	return &OptimizerImpl{}
}

const linkedFromEntrypoint = "linkedFromEntrypoint"

func (o *OptimizerImpl) Optimize(registry *jsonschema.Registry, entrypointMessage pgs.Message) error {
	entrypointSchemaRef := toRefId(entrypointMessage)
	entrypointSchema := registry.GetSchema(entrypointSchemaRef.String())

	if _, err := o.checkAndMarkSchemaToVisitable(registry, entrypointSchemaRef.String()); err != nil {
		return err
	}
	if err := o.visitSchema(registry, entrypointSchema); err != nil {
		return err
	}

	o.optimizeDefinitions(registry)
	return nil
}

func (o *OptimizerImpl) optimizeDefinitions(registry *jsonschema.Registry) {
	var deleteKeys []string
	for _, key := range registry.GetKeys() {
		schema := registry.GetSchema(key)
		if schema.GetExtrasItem(linkedFromEntrypoint) == nil || !schema.GetExtrasItem(linkedFromEntrypoint).(bool) {
			deleteKeys = append(deleteKeys, key)
		}
	}

	for _, key := range deleteKeys {
		registry.DeleteSchema(key)
	}
}

// return true if the first visit to schema
func (o *OptimizerImpl) checkAndMarkSchemaToVisitable(registry *jsonschema.Registry, ref string) (bool, error) {
	schema := registry.GetSchema(ref)
	if schema == nil {
		// visibility 会移除消息及其子节点；可见字段引用隐藏消息属于输入约束错误。
		return false, fmt.Errorf("schema not found: %s; check visibility_level for the entrypoint and referenced messages", ref)
	}

	rawValue := schema.GetExtrasItem(linkedFromEntrypoint)
	if rawValue == nil {
		schema.SetExtrasItem(linkedFromEntrypoint, true)
		return true, nil
	} else {
		return false, nil
	}
}

func (o *OptimizerImpl) visitSchema(registry *jsonschema.Registry, schema *jsonschema.Schema) error {
	if schema == nil {
		return nil
	}

	if !schema.Ref.IsEmpty() {
		first, err := o.checkAndMarkSchemaToVisitable(registry, schema.Ref.String())
		if err != nil {
			return err
		}
		if first {
			if err := o.visitSchema(registry, registry.GetSchema(schema.Ref.String())); err != nil {
				return err
			}
		}
	}
	for _, schemas := range []jsonschema.SchemaMap{
		schema.Definitions, schema.DependentSchemas, schema.Properties, schema.PatternProperties,
	} {
		if err := o.visitSchemaMap(registry, schemas); err != nil {
			return err
		}
	}
	for _, schemas := range [][]*jsonschema.Schema{
		schema.AllOf, schema.AnyOf, schema.OneOf, schema.PrefixItems,
		{schema.Not, schema.If, schema.Then, schema.Else, schema.Items, schema.Contains,
			schema.AdditionalProperties, schema.PropertyNames, schema.ContentSchema},
	} {
		if err := o.visitSchemaArray(registry, schemas); err != nil {
			return err
		}
	}
	return nil
}

func (o *OptimizerImpl) visitSchemaArray(registry *jsonschema.Registry, schemas []*jsonschema.Schema) error {
	for _, schema := range schemas {
		if err := o.visitSchema(registry, schema); err != nil {
			return err
		}
	}
	return nil
}

func (o *OptimizerImpl) visitSchemaMap(registry *jsonschema.Registry, schemaMap jsonschema.SchemaMap) error {
	if schemaMap == nil {
		return nil
	}
	for _, key := range schemaMap.Keys() {
		schema, _ := schemaMap.Get(key)
		if err := o.visitSchema(registry, schema); err != nil {
			return err
		}
	}
	return nil
}
