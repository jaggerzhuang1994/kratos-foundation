package bootstrap_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/bootstrap/consulconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
)

func TestLocalConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	t.Setenv("BOOTSTRAP_TEST_VALUE", "from-env")
	info := appinfo.New("test")
	for _, kind := range []string{"file", "directory", "glob", "missing", "invalid glob", "empty"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			switch kind {
			case "directory":
				path = filepath.Join(dir, "configs")
			case "glob":
				path = filepath.Join(dir, "*.yaml")
			case "invalid glob":
				path = "["
			case "empty":
				path = ""
			}
			sources := consulconfig.NewConfigSources(info, bootstrap.LocalConfigPath(path), "unused", consulconfig.NewDefaultLocalConfigPathsProvider(), func(bootstrap.RemoteConfigDirName, string, string) []string {
				t.Fatal("local called remote provider")
				return nil
			})
			spec := bootstrap.NewSpec(app.NewSpec(), server.NewSpec(), job.NewSpec(), sources)
			// 声明之后才创建文件/目录，验证路径判断与 I/O 都延迟到加载阶段。
			write := func(path, value string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "file":
				write(path, "answer: 42\n")
			case "directory":
				write(filepath.Join(path, info.Name()+".yaml"), "answer: 1\nbase: retained\n")
				write(filepath.Join(path, "local", info.Name()+".yaml"), "answer: 42\n")
				write(filepath.Join(path, "unrelated.yaml"), "invalid: [")
			case "glob":
				write(filepath.Join(dir, "a.yaml"), "answer: 1\n")
				write(filepath.Join(dir, "b.yaml"), "answer: 42\n")
			}
			manager, cleanup, err := bootstrap.NewConfigManager(spec)
			if cleanup != nil {
				defer cleanup()
			}
			if kind == "invalid glob" || kind == "empty" {
				if err == nil {
					t.Fatal("invalid path accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var value string
			if err := manager.Load("BOOTSTRAP_TEST_VALUE", &value); err != nil || value != "from-env" {
				t.Fatal(value, err)
			}
			if kind == "missing" {
				return
			}
			var answer int
			if err := manager.Load("answer", &answer); err != nil || answer != 42 {
				t.Fatalf("answer=%d err=%v", answer, err)
			}
			if kind == "directory" {
				if err := manager.Load("base", &value); err != nil || value != "retained" {
					t.Fatal(value, err)
				}
			}
		})
	}
}

func TestRemoteConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	info := appinfo.New("test")
	called := false
	sources := consulconfig.NewConfigSources(info, "unused.yaml", "services", func(bootstrap.LocalConfigPath, string, string) ([]string, error) {
		t.Fatal("remote called local provider")
		return nil, nil
	}, func(dir bootstrap.RemoteConfigDirName, name, environment string) []string {
		called = true
		if dir != "services" || name != info.Name() || environment != "prod" {
			t.Fatalf("arguments=%s %s %s", dir, name, environment)
		}
		return []string{"configs/services/" + name + ".yaml"}
	})
	// ConfigSources 只描述依赖；环境直到 NewSpec 才固定。
	t.Setenv("APP_ENV", "prod")
	spec := bootstrap.NewSpec(app.NewSpec(), server.NewSpec(), job.NewSpec(), sources)
	if called {
		t.Fatal("declaration resolved paths")
	}
	t.Setenv("APP_ENV", "local") // 环境在构造时固定，不会因随后变化切换后端。
	t.Setenv("DISABLE_CONSUL", "true")
	manager, cleanup, err := bootstrap.NewConfigManager(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if manager == nil || !called {
		t.Fatal("remote loader not used")
	}
	t.Setenv("APP_ENV", "prod")
	spec = bootstrap.NewSpec(app.NewSpec(), server.NewSpec(), job.NewSpec(), consulconfig.NewConfigSources(info, "unused", "", consulconfig.NewDefaultLocalConfigPathsProvider(), consulconfig.NewDefaultRemoteConfigPathsProvider()))
	if _, _, err := bootstrap.NewConfigManager(spec); err == nil {
		t.Fatal("missing directory accepted")
	}
}

func TestLocalProviderError(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	failure := errors.New("path lookup failed")
	sources := consulconfig.NewConfigSources(appinfo.New("test"), "unused", "unused", func(bootstrap.LocalConfigPath, string, string) ([]string, error) { return nil, failure }, consulconfig.NewDefaultRemoteConfigPathsProvider())
	spec := bootstrap.NewSpec(app.NewSpec(), server.NewSpec(), job.NewSpec(), sources)
	if _, _, err := bootstrap.NewConfigManager(spec); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}

func TestIncompleteConfigSources(t *testing.T) {
	assertBootstrapPanic(t, "bootstrap: incomplete config sources", func() {
		bootstrap.NewSpec(app.NewSpec(), server.NewSpec(), job.NewSpec(), bootstrap.ConfigSources{LocalPath: "app.yaml"})
	})
}
