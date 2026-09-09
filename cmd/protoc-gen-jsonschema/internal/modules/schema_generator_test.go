package modules

import (
	pgs "github.com/lyft/protoc-gen-star/v2"
	"google.golang.org/protobuf/types/known/anypb"
	"reflect"
	"testing"
)

type fqdn string

func (f fqdn) FullyQualifiedName() string { return string(f) }

func TestGeneratorPureHelpersPreserveReferencesAndTypes(t *testing.T) {
	if got := toRefId(fqdn("package.Message")); got != "package.Message" {
		t.Fatalf("toRefId = %q", got)
	}
	if !isInt64(pgs.Int64T) || !isInt64(pgs.Fixed64T) || isInt64(pgs.Int32T) {
		t.Fatal("isInt64 classification is wrong")
	}
	remaining := deletePropertyInRequired([]string{"a", "b", "a"}, "a")
	if len(remaining) != 1 || remaining[0] != "b" {
		t.Fatalf("remaining properties = %#v", remaining)
	}
}

func TestParseScalarValueFromAny(t *testing.T) {
	tests := []struct {
		name    string
		value   *anypb.Any
		want    any
		wantErr bool
	}{
		{name: "nil Any"},
		{name: "empty Any", value: &anypb.Any{}},
		{name: "string", value: &anypb.Any{Value: []byte(`"text"`)}, want: "text"},
		{name: "number", value: &anypb.Any{Value: []byte(`42.5`)}, want: 42.5},
		{name: "boolean", value: &anypb.Any{Value: []byte(`true`)}, want: true},
		{name: "null", value: &anypb.Any{Value: []byte(`null`)}},
		{name: "array", value: &anypb.Any{Value: []byte(`[1,"two"]`)}, want: []any{float64(1), "two"}},
		{name: "object", value: &anypb.Any{Value: []byte(`{"key":"value"}`)}, want: map[string]any{"key": "value"}},
		{name: "malformed", value: &anypb.Any{Value: []byte(`{`)}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseScalaValueFromAny(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatal("parseScalaValueFromAny() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseScalaValueFromAny() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("parseScalaValueFromAny() = %#v, want %#v", got, test.want)
			}
		})
	}
}
