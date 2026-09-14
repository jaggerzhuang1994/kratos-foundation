package file

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

func TestNewSourcesPreservesFilePriorityInManager(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first.yaml")
	second := filepath.Join(directory, "second.yaml")
	if err := os.WriteFile(first, []byte("value: one\n"), 0o600); err != nil {
		t.Fatalf("write first config: %v", err)
	}
	if err := os.WriteFile(second, []byte("value: two\n"), 0o600); err != nil {
		t.Fatalf("write second config: %v", err)
	}
	paths := PathList{second, directory, filepath.Join(directory, "first*")}

	sources, err := NewSources(paths)
	if err != nil {
		t.Fatalf("NewSources() error = %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("NewSources() count = %d, want 2", len(sources))
	}
	for index, source := range sources {
		values, err := source.Load()
		if err != nil {
			t.Fatalf("source %d Load() error = %v", index, err)
		}
		if len(values) != 1 {
			t.Errorf("source %d value count = %d, want 1", index, len(values))
		}
	}

	manager, cleanup, err := config.NewManager(config.Sources(sources))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	var value string
	if err := manager.Load("value", &value); err != nil {
		t.Fatal(err)
	}
	if value != "one" {
		t.Fatalf("effective value = %q, want first.yaml loaded after second.yaml", value)
	}
}

func TestNewSourcesHandlesEmptyUnmatchedInvalidAndDuplicatePaths(t *testing.T) {
	// 本测试串行执行，退出时恢复全局绑定，验证构造事件使用当前全局 Logger。
	previous := log.GetLogger()
	var output bytes.Buffer
	log.SetLogger(kratoslog.NewStdLogger(&output))
	t.Cleanup(func() { log.SetLogger(previous) })

	empty, err := NewSources(nil)
	if err != nil || empty != nil {
		t.Fatalf("NewSources(nil) = %#v, %v; want nil, nil", empty, err)
	}
	unmatched, err := NewSources(PathList{filepath.Join(t.TempDir(), "*.yaml")})
	if err != nil || unmatched != nil {
		t.Fatalf("NewSources(unmatched) = %#v, %v; want nil, nil", unmatched, err)
	}
	if _, err := NewSources(PathList{"["}); err == nil {
		t.Fatal("NewSources(invalid glob) error = nil")
	}

	filename := filepath.Join(t.TempDir(), "config[1]*.yaml")
	if err := os.WriteFile(filename, []byte("value: one\n"), 0o600); err != nil {
		t.Fatalf("write duplicate config: %v", err)
	}
	deduplicated, err := NewSources(PathList{filename, filename})
	if err != nil {
		t.Fatalf("NewSources(duplicates) error = %v", err)
	}
	if len(deduplicated) != 1 {
		t.Fatalf("NewSources(duplicates) count = %d, want 1", len(deduplicated))
	}
	for _, event := range []string{"No local configuration files matched the pattern", "Matched local configuration files"} {
		if !strings.Contains(output.String(), event) {
			t.Fatalf("global log missing %q: %s", event, output.String())
		}
	}

}

func TestNewSourcesResolvesPaths(t *testing.T) {
	dir := t.TempDir()
	for name, value := range map[string]string{"a.yaml": "first", "z.yaml": "last", "ignored.yml": "wrong"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("value: "+value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "nested.yaml"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested.yaml", "bad.yaml"), []byte("value: nested"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, want string
		count      int
	}{
		{dir, "last", 2},
		{dir + string(os.PathSeparator), "last", 2},
		{filepath.Join(dir, "a.yaml"), "first", 1},
		{filepath.Join(dir, "*.yaml"), "last", 2},
		{filepath.Join(dir, "[az].yaml"), "last", 2},
		{filepath.Join(dir, "?.yaml"), "last", 2},
		{filepath.Join(dir, "*", "*.yaml"), "nested", 1},
	} {
		sources, err := NewSources(PathList{tc.path})
		if err != nil || len(sources) != tc.count {
			t.Fatalf("sources=%v err=%v", sources, err)
		}
		manager, cleanup, err := config.NewManager(config.Sources(sources))
		if err != nil {
			t.Fatal(err)
		}
		var value string
		err = manager.Load("value", &value)
		cleanup()
		if err != nil || value != tc.want {
			t.Fatalf("value=%s err=%v", value, err)
		}
	}
	for _, path := range []string{"", filepath.Join(dir, "[")} {
		if _, err := NewSources(PathList{path}); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	// 显式 glob 不按扩展名过滤；格式是否可解码由 Manager 决定。
	all, err := NewSources(PathList{filepath.Join(dir, "*")})
	if err != nil || len(all) != 3 {
		t.Fatalf("all files=%v err=%v", all, err)
	}
	literal := filepath.Join(dir, "literal[1].yaml")
	if err := os.WriteFile(literal, []byte("value: literal"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{literal, filepath.Join(dir, `literal\[1\].yaml`)} {
		sources, err := NewSources(PathList{pattern})
		if err != nil || len(sources) != 1 {
			t.Fatalf("escaped path=%q sources=%v err=%v", pattern, sources, err)
		}
		values, err := sources[0].Load()
		if err != nil || len(values) != 1 || string(values[0].Value) != "value: literal" {
			t.Fatalf("literal values=%v err=%v", values, err)
		}
	}
}

func TestNewSourcesSkipsUnmatchedEntries(t *testing.T) {
	for _, path := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "missing*")} {
		sources, err := NewSources(PathList{path})
		if err != nil || len(sources) != 0 {
			t.Fatalf("path=%q sources=%v err=%v", path, sources, err)
		}
	}
}

func TestDirectoryExpansionKeepsSelectedFiles(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a.yaml")
	if err := os.WriteFile(first, []byte("value: first"), 0600); err != nil {
		t.Fatal(err)
	}
	sources, err := NewSources(PathList{dir})
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources=%v err=%v", sources, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.yaml"), []byte("value: new"), 0600); err != nil {
		t.Fatal(err)
	}
	values, err := sources[0].Load()
	if err != nil || len(values) != 1 || values[0].Key != "a.yaml" {
		t.Fatalf("expanded source included new file: values=%v err=%v", values, err)
	}
}
