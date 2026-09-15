package consul

import (
	"os"
	"path/filepath"
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
			spec, err := NewSpec(appinfo.New("test"), bootstrap.LocalConfigPath(path), func(string, string) []string {
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
	info := appinfo.New("test")
	called := false
	spec, err := NewSpec(info, "unused-local.yaml", func(name, environment string) []string {
		called = true
		if name != info.Name() || environment != "prod" {
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
