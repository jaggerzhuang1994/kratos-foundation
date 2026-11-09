package file

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// newOrderedFileManager 以 base、override 两个文件来源构造 Manager，override 优先级更高。
func newOrderedFileManager(t *testing.T) (config.Manager, string) {
	t.Helper()
	t.Setenv("CONFIG_POLL_INTERVAL", "10ms")
	dir := t.TempDir()
	base := filepath.Join(dir, "base.yaml")
	override := filepath.Join(dir, "override.yaml")
	if err := os.WriteFile(base, []byte("a: base\nb: one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(override, []byte("a: override\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sources, err := NewSources(PathList{base, override})
	if err != nil {
		t.Fatal(err)
	}
	manager, cleanup, err := config.NewManager(config.Sources(sources))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if got := loadString(t, manager, "a"); got != "override" {
		t.Fatalf("initial a = %q, want override", got)
	}
	return manager, base
}

func loadString(t *testing.T, manager config.Manager, key string) string {
	t.Helper()
	var value string
	if err := manager.Load(key, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// 前面的来源重载后，后面来源覆盖的键仍应生效。
func TestManagerKeepsLaterFileOverrideWhenEarlierFileReloads(t *testing.T) {
	manager, base := newOrderedFileManager(t)
	if err := os.WriteFile(base, []byte("a: base\nb: two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for loadString(t, manager, "b") != "two" {
		if time.Now().After(deadline) {
			t.Fatal("base file reload not observed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := loadString(t, manager, "a"); got != "override" {
		t.Fatalf("a = %q after base reload, want override", got)
	}
}

// 只改 mtime 的 touch 不应改变有效配置，也不应发出变化通知。
func TestManagerIgnoresTouchOfEarlierFile(t *testing.T) {
	var changed atomic.Int32
	t.Cleanup(log.SetLogger(fileLogFunc(func(_ kratoslog.Level, fields ...any) error {
		for i := 0; i+1 < len(fields); i += 2 {
			if fields[i] == "event" && fields[i+1] == "config.snapshot.changed" {
				changed.Add(1)
			}
		}
		return nil
	})))
	manager, base := newOrderedFileManager(t)
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(base, later, later); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if got := loadString(t, manager, "a"); got != "override" {
		t.Fatalf("a = %q after touch, want override", got)
	}
	if n := changed.Load(); n != 0 {
		t.Fatalf("config changed logged %d times after touch", n)
	}
}
