package consulconfig

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
)

func TestDefaultRemoteConfigPaths(t *testing.T) {
	provider := NewDefaultRemoteConfigPathsProvider()
	info := appinfo.New("test")
	want := []string{
		"configs/common*.yaml", "configs/prod/common*.yaml",
		"configs/services/orders.yaml", "configs/services/orders/*.yaml",
		"configs/services/prod/orders.yaml", "configs/services/orders/prod/*.yaml",
		"secrets/common*.yaml", "secrets/prod/common*.yaml",
		"secrets/services/orders.yaml", "secrets/services/orders/*.yaml",
		"secrets/services/prod/orders.yaml", "secrets/services/orders/prod/*.yaml",
	}
	name := bootstrap.RemoteConfigName("orders")
	got := provider(info, "prod", "services", name)
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
	if !slices.Equal(provider(info, "prod", "services", "orders"), want) {
		t.Fatal("shared path slice")
	}
}

func TestDefaultLocalConfigPaths(t *testing.T) {
	info := appinfo.New("test")
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
		{"directory", dir, []string{filepath.Join(dir, info.Name()+".yaml"), filepath.Join(dir, "local", info.Name()+".yaml")}},
		{"glob", filepath.Join(t.TempDir(), "*.yaml"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.want
			if want == nil {
				want = []string{tc.path}
			}
			got, err := NewDefaultLocalConfigPathsProvider()(info, "local", bootstrap.LocalConfigPath(tc.path))
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("paths=%v error=%v want=%v", got, err, want)
			}
		})
	}
	if _, err := NewDefaultLocalConfigPathsProvider()(info, "local", "invalid\x00path"); err == nil {
		t.Fatal("stat error hidden")
	}
}
