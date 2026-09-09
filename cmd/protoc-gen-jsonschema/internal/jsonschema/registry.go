package jsonschema

import (
	"slices"

	"github.com/iancoleman/orderedmap"
)

type Registry struct {
	schemasByName SchemaMap
}

func NewRegistry() *Registry {
	return &Registry{schemasByName: NewOrderedSchemaMap()}
}

func (r *Registry) AddSchema(id string, schema *Schema) {
	r.schemasByName.Set(id, schema)
}

func (r *Registry) HasSchema(id string) bool {
	_, found := r.schemasByName.Get(id)
	return found
}

func (r *Registry) GetSchema(id string) *Schema {
	schema, _ := r.schemasByName.Get(id)
	return schema
}

func (r *Registry) GetKeys() []string {
	return r.schemasByName.Keys()
}

func (r *Registry) DeleteSchema(id string) {
	r.schemasByName.Delete(id)
}

func (r *Registry) SortSchemas() {
	sortedKeys := slices.Clone(r.GetKeys())
	slices.Sort(sortedKeys)

	for _, key := range sortedKeys {
		schema := r.GetSchema(key)
		r.DeleteSchema(key)
		r.AddSchema(key, schema)
	}
}

func DeepCopyRegistry(registry *Registry) *Registry {
	newRegistry := NewRegistry()
	newRegistry.schemasByName = DeepCopyMap(registry.schemasByName)
	return newRegistry
}

type SchemaMap interface {
	Get(key string) (*Schema, bool)
	Set(key string, value *Schema)
	Keys() []string
	Delete(key string)
}

type orderedSchemaMap struct {
	memory *orderedmap.OrderedMap
}

func NewOrderedSchemaMap() SchemaMap {
	return &orderedSchemaMap{memory: orderedmap.New()}
}

func (m *orderedSchemaMap) Get(key string) (*Schema, bool) {
	if m == nil {
		return nil, false
	}

	v, found := m.memory.Get(key)
	if !found {
		return nil, false
	}
	return v.(*Schema), true
}

func (m *orderedSchemaMap) Set(key string, value *Schema) {
	m.memory.Set(key, value)
}

func (m *orderedSchemaMap) Keys() []string {
	if m == nil {
		return nil
	}
	return m.memory.Keys()
}

func (m *orderedSchemaMap) Delete(key string) {
	m.memory.Delete(key)
}
