package decoder

import (
	"errors"
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

func TestReservedFieldsAreRejectedWithoutRejectingBusinessExtensions(t *testing.T) {
	descriptor := new(kratos_foundation_pb.Config).ProtoReflect().Descriptor()
	for _, test := range []struct {
		name  string
		value any
		path  string
	}{
		{"removed null", map[string]any{"metrics": nil}, "metrics"},
		{"alias", map[string]any{"app": map[string]any{"disableRegistrar": false}}, "app.disableRegistrar"},
		{"repeated", map[string]any{"server": map[string]any{"middleware": map[string]any{"deadline": map[string]any{"routes": []any{map[string]any{"response_reserve": "1s"}}}}}}, "server.middleware.deadline.routes[0].response_reserve"},
		{"unknown business", map[string]any{"business": map[string]any{"log": true}}, ""},
		{"metadata keys", map[string]any{"app": map[string]any{"metadata": map[string]any{"log": "allowed"}}}, ""},
		{"scalar", "invalid shape handled by decoder", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateReserved(test.value, descriptor, "")
			if test.path == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, ErrRemovedField) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	var target config_pb.Server
	d, err := New(&target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Apply(map[string]any{"log": map[string]any{"password": "secret"}}, true, &target); !errors.Is(err, ErrRemovedField) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe or missing diagnostic: %v", err)
	}
}
