package consulconfig

import (
	"path"
	"slices"
	"strings"
	"testing"
)

func TestDefaultRemoteConfigPaths(t *testing.T) {
	prefixes := ConsulConfigPrefix{"configs", "secrets"}
	paths := ConsulConfigPaths{
		"common*.yaml", "{{env}}/common*.yaml",
		"services/{{app}}.yaml", "services/{{app}}/*.yaml",
		"services/{{env}}/{{app}}.yaml", "services/{{app}}/{{env}}/*.yaml",
	}
	got := NewDefaultRemoteConfigPaths(prefixes, paths)
	render := strings.NewReplacer("{{env}}", "prod", "{{app}}", "orders")
	for i := range got {
		got[i] = render.Replace(got[i])
	}
	want := []string{
		"configs/common*.yaml", "configs/prod/common*.yaml",
		"configs/services/orders.yaml", "configs/services/orders/*.yaml",
		"configs/services/prod/orders.yaml", "configs/services/orders/prod/*.yaml",
		"secrets/common*.yaml", "secrets/prod/common*.yaml",
		"secrets/services/orders.yaml", "secrets/services/orders/*.yaml",
		"secrets/services/prod/orders.yaml", "secrets/services/orders/prod/*.yaml",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("paths=%v want=%v", got, want)
	}
	// 环境单文件与片段均不能串入其他环境。
	for _, key := range []string{"configs/services/test/orders.yaml", "configs/services/orders/test/app.yaml", "secrets/services/test/orders.yaml", "secrets/services/orders/test/app.yaml"} {
		for _, pattern := range got {
			matched, err := path.Match(pattern, key)
			if err != nil || matched {
				t.Fatalf("pattern=%q matched other environment key=%q: %v", pattern, key, err)
			}
		}
	}
	got[0] = "changed"
	if prefixes[0] != "configs" || paths[0] != "common*.yaml" || NewDefaultRemoteConfigPaths(prefixes, paths)[0] != "configs/common*.yaml" {
		t.Fatal("inputs or next result were modified")
	}
}

func TestDefaultRemoteConfigPathsNestedPrefix(t *testing.T) {
	got := NewDefaultRemoteConfigPaths(
		ConsulConfigPrefix{"configs/services/", "secrets/{{env}}/services"},
		ConsulConfigPaths{"{{app}}.yaml", "{{app}}/*.yaml"},
	)
	want := []string{
		"configs/services/{{app}}.yaml",
		"configs/services/{{app}}/*.yaml",
		"secrets/{{env}}/services/{{app}}.yaml",
		"secrets/{{env}}/services/{{app}}/*.yaml",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("paths=%v want=%v", got, want)
	}
}

func TestDefaultRemoteConfigPathsEmptyInputs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		prefixes ConsulConfigPrefix
		paths    ConsulConfigPaths
	}{
		{"no prefixes", nil, ConsulConfigPaths{"common*.yaml"}},
		{"no patterns", ConsulConfigPrefix{"configs"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewDefaultRemoteConfigPaths(tc.prefixes, tc.paths); len(got) != 0 {
				t.Fatalf("paths=%v, want empty", got)
			}
		})
	}
}
