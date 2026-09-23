package bootstrap_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/bootstrap/consulconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
)

func TestLocalConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	t.Setenv("BOOTSTRAP_TEST_VALUE", "from-env")
	info := appinfo.New("test")
	for _, kind := range []string{"file", "application paths", "directory", "glob", "template glob", "missing", "invalid glob"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			switch kind {
			case "directory", "application paths":
				path = filepath.Join(dir, "configs")
			case "glob":
				path = filepath.Join(dir, "*.yaml")
			case "template glob":
				path = filepath.Join(dir, "{{env}}", "{{app}}", "{{version}}", "*.yaml")
			case "invalid glob":
				path = "["
			}
			sources := consulconfig.NewConfigSources(info, bootstrap.LocalConfigPaths{path}, bootstrap.RemoteConfigPaths{"{{unused}}"})
			if kind == "application paths" {
				sources.LocalPaths = bootstrap.LocalConfigPaths{filepath.Join(path, "{{app}}.yaml"), filepath.Join(path, "{{env}}", "{{app}}.yaml")}
			}
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
				write(filepath.Join(path, "a.yaml"), "answer: 1\n")
				write(filepath.Join(path, "b.yaml"), "answer: 42\n")
				write(filepath.Join(path, "nested", "ignored.yaml"), "answer: 0\n")
			case "application paths":
				write(filepath.Join(path, info.Name()+".yaml"), "answer: 1\nbase: retained\n")
				write(filepath.Join(path, "local", info.Name()+".yaml"), "answer: 42\n")
				write(filepath.Join(path, "unrelated.yaml"), "invalid: [")
			case "template glob":
				write(filepath.Join(dir, "local", info.Name(), info.Version(), "a.yaml"), "answer: 1\n")
				write(filepath.Join(dir, "local", info.Name(), info.Version(), "b.yaml"), "answer: 42\n")
			case "glob":
				write(filepath.Join(dir, "a.yaml"), "answer: 1\n")
				write(filepath.Join(dir, "b.yaml"), "answer: 42\n")
			}
			manager, cleanup, err := bootstrap.NewConfigManager(spec)
			if cleanup != nil {
				defer cleanup()
			}
			if kind == "invalid glob" {
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
			if kind == "application paths" {
				if err := manager.Load("base", &value); err != nil || value != "retained" {
					t.Fatal(value, err)
				}
			}
		})
	}
}

func TestRemoteConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	info := appinfo.New("v2")
	original := bootstrap.RemoteConfigPaths{"configs/{{env}}/{{app}}/{{version}}/*.yaml", "{{if eq env `prod`}}secrets/shared-orders.yaml{{else}}secrets/other.yaml{{end}}"}
	sources := consulconfig.NewConfigSources(info, bootstrap.LocalConfigPaths{"{{unused}}"}, original)
	called := false
	sources.RemoteSource = func(paths ...string) config.SourceLoader {
		called = true
		want := []string{"configs/prod/" + info.Name() + "/v2/*.yaml", "secrets/shared-orders.yaml"}
		if !slices.Equal(paths, want) {
			t.Fatalf("paths=%v want=%v", paths, want)
		}
		return func() (config.Sources, error) { return nil, nil }
	}
	// 环境在 NewSpec 固定；路径列表也复制，避免后续业务修改影响加载器。
	t.Setenv("APP_ENV", "prod")
	spec := bootstrap.NewSpec(app.NewSpec(), server.NewSpec(), job.NewSpec(), sources)
	if called {
		t.Fatal("declaration resolved paths")
	}
	original[0] = "changed"
	t.Setenv("APP_ENV", "local")
	manager, cleanup, err := bootstrap.NewConfigManager(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if manager == nil || !called {
		t.Fatal("remote loader not used")
	}
}

func TestConfigPathTemplateErrors(t *testing.T) {
	for _, environment := range []string{"local", "prod"} {
		t.Run(environment, func(t *testing.T) {
			t.Setenv("APP_ENV", environment)
			for _, tc := range []struct{ name, pattern, want string }{
				{"parse", "{{", "parse config path template 1"},
				{"unknown function", "{{unknown}}", "parse config path template 1"},
				{"execution", "{{env `unexpected`}}", "execute config path template 1"},
				{"empty", "", "rendered an empty path"},
				{"whitespace", "  ", "rendered an empty path"},
				{"empty result", "{{if eq env `absent`}}config.yaml{{end}}", "rendered an empty path"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					paths := []string{"valid.yaml", tc.pattern}
					source := func(...string) config.SourceLoader {
						t.Fatal("template failure called source")
						return nil
					}
					spec := bootstrap.NewSpec(app.NewSpec(), server.NewSpec(), job.NewSpec(), bootstrap.ConfigSources{
						AppInfo: appinfo.New("v1"), LocalPaths: paths, RemotePaths: paths,
						LocalSource: source, RemoteSource: source,
					})
					if _, _, err := bootstrap.NewConfigManager(spec); err == nil || !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("err=%v want=%s", err, tc.want)
					}
				})
			}
		})
	}
}

func TestConfigSourceFailureAndEmptyPaths(t *testing.T) {
	failure := errors.New("source failed")
	for _, environment := range []string{"local", "prod"} {
		t.Run(environment, func(t *testing.T) {
			t.Setenv("APP_ENV", environment)
			source := func(...string) config.SourceLoader {
				return func() (config.Sources, error) { return nil, failure }
			}
			sources := bootstrap.ConfigSources{AppInfo: appinfo.New("v1"), LocalSource: source, RemoteSource: source}
			spec := bootstrap.NewSpec(app.NewSpec(), server.NewSpec(), job.NewSpec(), sources)
			// 空列表不调用来源构造函数，仍可从 env 加载配置。
			manager, cleanup, err := bootstrap.NewConfigManager(spec)
			if err != nil {
				t.Fatal(err)
			}
			cleanup()
			if manager == nil {
				t.Fatal("nil manager")
			}
			sources.LocalPaths, sources.RemotePaths = []string{"config.yaml"}, []string{"config.yaml"}
			spec = bootstrap.NewSpec(app.NewSpec(), server.NewSpec(), job.NewSpec(), sources)
			if _, _, err := bootstrap.NewConfigManager(spec); !errors.Is(err, failure) {
				t.Fatal(err)
			}
		})
	}
}

func TestDisabledConsulConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("DISABLE_CONSUL", "true")
	sources := consulconfig.NewConfigSources(appinfo.New("v1"), nil, bootstrap.RemoteConfigPaths{"configs/{{app}}/*.yaml"})
	spec := bootstrap.NewSpec(app.NewSpec(), server.NewSpec(), job.NewSpec(), sources)
	manager, cleanup, err := bootstrap.NewConfigManager(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if manager == nil {
		t.Fatal("nil manager")
	}
}

func TestIncompleteConfigSources(t *testing.T) {
	assertBootstrapPanic(t, "bootstrap: incomplete config sources", func() {
		bootstrap.NewSpec(app.NewSpec(), server.NewSpec(), job.NewSpec(), bootstrap.ConfigSources{LocalPaths: []string{"app.yaml"}})
	})
}
