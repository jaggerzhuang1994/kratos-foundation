package modules

import (
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/jsonschema"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/proto"
	"testing"
)

func TestKeywordFillersApplyOnlyDeclaredValues(t *testing.T) {
	minimum, exclusiveMax, multiple := 2.0, 9.0, int32(3)
	minLength, unique := uint32(2), true
	schema := &jsonschema.Schema{}
	fillSchemaByNumericKeywords(schema, &proto.NumericKeywords{Min: &proto.NumericKeywords_InclusiveMinimum{InclusiveMinimum: minimum}, Max: &proto.NumericKeywords_ExclusiveMaximum{ExclusiveMaximum: exclusiveMax}, MultipleOf: &multiple})
	fillSchemaByStringKeywords(schema, &proto.StringKeywords{MinLength: &minLength, Enum: []string{"a", "b"}})
	fillSchemaByArrayKeywords(schema, &proto.ArrayKeywords{UniqueItems: &unique})
	if schema.Minimum == nil || *schema.Minimum != minimum || schema.ExclusiveMaximum == nil || *schema.ExclusiveMaximum != exclusiveMax || schema.MultipleOf == nil || *schema.MultipleOf != 3 || schema.MinLength == nil || *schema.MinLength != 2 || !*schema.UniqueItems || len(schema.Enum) != 2 {
		t.Fatalf("keywords not copied: %#v", schema)
	}
}
