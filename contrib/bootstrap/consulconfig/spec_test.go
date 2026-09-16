package consulconfig

import (
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
)

func TestNewSpecLocalConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	t.Setenv("BOOTSTRAP_TEST_VALUE", "from-env")
	for _, kind := range []string{"file", "missing", "invalid pattern"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if kind == "invalid pattern" {
				path = "["
			}
			spec, err := NewSpec(app.NewSpec(), server.NewSpec(), job.NewSpec(), bootstrap.LocalConfigPath(path), "unused-remote", func(string, string) []string {
				t.Fatal("local configuration called remote paths provider")
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			// 在声明之后才创建文件，验证来源读取延迟到 Configuration 阶段。
			if kind == "file" {
				if err := os.WriteFile(path, []byte("answer: 42\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			manager, cleanup, err := bootstrap.NewConfigManager(spec)
			if kind == "invalid pattern" {
				if cleanup != nil {
					cleanup()
				}
				if err == nil {
					t.Fatal("invalid configuration pattern was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			var value string
			if err := manager.Load("BOOTSTRAP_TEST_VALUE", &value); err != nil || value != "from-env" {
				t.Fatal(value, err)
			}
			if kind == "missing" {
				return
			}
			var answer int
			if err := manager.Load("answer", &answer); err != nil || answer != 42 {
				t.Fatal(answer, err)
			}
		})
	}
}

func TestNewSpecAllowsDisabledConsul(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("BOOTSTRAP_TEST_VALUE", "from-env")
	configName := RemoteConfigName("shared-orders")
	called := false
	spec, err := NewSpec(app.NewSpec(), server.NewSpec(), job.NewSpec(), "unused-local.yaml", configName, func(name, environment string) []string {
		called = true
		if name != string(configName) || environment != "prod" {
			t.Fatalf("unexpected provider arguments: %q, %q", name, environment)
		}
		return []string{"custom/" + environment + "/" + name + ".yaml"}
	})
	if !called {
		t.Fatal("remote paths provider was not called")
	}
	if err != nil {
		t.Fatal(err)
	}
	// 声明不触发客户端初始化；执行时才读取禁用状态，不回退本地路径。
	t.Setenv("DISABLE_CONSUL", "true")
	manager, cleanup, err := bootstrap.NewConfigManager(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var value string
	if err := manager.Load("BOOTSTRAP_TEST_VALUE", &value); err != nil || value != "from-env" {
		t.Fatal(value, err)
	}
}

func TestDefaultRemoteConfigProviders(t *testing.T) {
	info := appinfo.New("test")
	if got := NewDefaultRemoteConfigName(info); string(got) != info.Name() {
		t.Fatalf("default name = %q, want %q", got, info.Name())
	}
	provider := NewDefaultRemoteConfigPathsProvider()
	want := []string{
		"configs/common*.yaml", "configs/prod/common*.yaml",
		"secrets/common*.yaml", "secrets/prod/common*.yaml",
		"configs/shared-orders/*.yaml", "configs/shared-orders/prod/*.yaml",
		"secrets/shared-orders/*.yaml", "secrets/shared-orders/prod/*.yaml",
	}
	got := provider("shared-orders", "prod")
	if !slices.Equal(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	got[0] = "changed"
	if next := provider("shared-orders", "prod"); !slices.Equal(next, want) {
		t.Fatalf("paths shared between calls: %v", next)
	}
}
