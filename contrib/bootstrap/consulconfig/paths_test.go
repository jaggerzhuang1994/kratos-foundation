package consulconfig

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
)

func TestDefaultRemoteConfigPaths(t *testing.T) {
	provider := NewDefaultRemoteConfigPathsProvider()
	want := []string{
		"configs/common*.yaml", "configs/prod/common*.yaml",
		"configs/services/orders.yaml", "configs/services/orders/*.yaml",
		"configs/services/prod/orders.yaml", "configs/services/prod/orders/*.yaml",
		"secrets/common*.yaml", "secrets/prod/common*.yaml",
		"secrets/services/orders.yaml", "secrets/services/orders/*.yaml",
		"secrets/services/prod/orders.yaml", "secrets/services/prod/orders/*.yaml",
	}
	got := provider("services", "orders", "prod")
	if !slices.Equal(got, want) {
		t.Fatalf("paths=%v want=%v", got, want)
	}
	got[0] = "changed"
	if !slices.Equal(provider("services", "orders", "prod"), want) {
		t.Fatal("shared path slice")
	}
}

func TestDefaultLocalConfigPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "literal[dir]")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "literal[file].yaml")
	if err := os.WriteFile(file, []byte("answer: 42"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		want       []string
	}{
		{"file", file, []string{file}},
		{"directory", dir, []string{filepath.Join(dir, "orders.yaml"), filepath.Join(dir, "local", "orders.yaml")}},
		{"glob", filepath.Join(t.TempDir(), "*.yaml"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.want
			if want == nil {
				want = []string{tc.path}
			}
			got, err := NewDefaultLocalConfigPathsProvider()(bootstrap.LocalConfigPath(tc.path), "orders", "local")
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("paths=%v error=%v want=%v", got, err, want)
			}
		})
	}
	if _, err := NewDefaultLocalConfigPathsProvider()("invalid\x00path", "orders", "local"); err == nil {
		t.Fatal("stat error hidden")
	}
}
