package file

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

type watchResult struct {
	values []*config.KeyValue
	err    error
}

func nextFileUpdate(watcher config.Watcher) <-chan watchResult {
	result := make(chan watchResult, 1)
	go func() {
		values, err := watcher.Next()
		result <- watchResult{values, err}
	}()
	return result
}

func newFileWatcher(t *testing.T, path string) config.Watcher {
	t.Helper()
	sources, err := NewSources(PathList{path})
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources=%v err=%v", sources, err)
	}
	watcher, err := sources[0].Watch()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := watcher.Stop(); err != nil {
			t.Error(err)
		}
	})
	return watcher
}

func waitFileValue(t *testing.T, watcher config.Watcher, expected string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case result := <-nextFileUpdate(watcher):
			if result.err != nil {
				t.Fatal(result.err)
			}
			if len(result.values) == 1 && string(result.values[0].Value) == expected {
				return
			}
		case <-deadline:
			t.Fatal("file watcher did not publish expected snapshot")
		}
	}
}

func TestFileWatcherSurvivesRemovalRecreationAndAtomicReplacement(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(path, []byte("value: initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	watcher := newFileWatcher(t, path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	pending := nextFileUpdate(watcher)
	select {
	case result := <-pending:
		t.Fatalf("temporary removal terminated or published missing file: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}
	if err := os.WriteFile(path, []byte("value: restored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-pending:
		if result.err != nil || len(result.values) != 1 || string(result.values[0].Value) != "value: restored\n" {
			t.Fatalf("restored snapshot=%v err=%v", result.values, result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("recreated file never recovered")
	}
	replacement := filepath.Join(directory, "replacement.tmp")
	if err := os.WriteFile(replacement, []byte("value: replaced\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	waitFileValue(t, watcher, "value: replaced\n")
	if err := os.WriteFile(path, []byte("value: final\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFileValue(t, watcher, "value: final\n")
}

func TestFileWatcherStopCancelsMissingFileRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("value: initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	watcher := newFileWatcher(t, path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	result := nextFileUpdate(watcher)
	if err := watcher.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-result:
		if !errors.Is(value.err, context.Canceled) {
			t.Fatalf("Next after Stop = %v", value.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel missing-file wait")
	}
}

func TestFileWatcherSurvivesParentDirectoryReplacement(t *testing.T) {
	directory := t.TempDir()
	parent := filepath.Join(directory, "mounted")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "config.yaml")
	if err := os.WriteFile(path, []byte("value: initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	watcher := newFileWatcher(t, path)
	if err := os.Rename(parent, filepath.Join(directory, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("value: replacement\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFileValue(t, watcher, "value: replacement\n")
	if err := os.WriteFile(path, []byte("value: final\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFileValue(t, watcher, "value: final\n")
}

func TestExpandedDirectoryFilesBindReplacementsBeforePublishing(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "config")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(path, []byte("value: initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	watcher := newFileWatcher(t, directory)

	// 目录已展开为单文件源，替换文件后必须先绑定新目标再发布快照。
	replacement := filepath.Join(directory, ".replacement.yaml")
	if err := os.WriteFile(replacement, []byte("value: replaced\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	waitFileValue(t, watcher, "value: replaced\n")
	// 发布后立即写入，验证新目标已被监听。
	if err := os.WriteFile(path, []byte("value: immediately-updated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFileValue(t, watcher, "value: immediately-updated\n")

	// 父目录整体替换后仍需跟踪已选中的同名文件。
	if err := os.Rename(directory, filepath.Join(root, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("value: new-directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFileValue(t, watcher, "value: new-directory\n")
	if err := os.WriteFile(path, []byte("value: final\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFileValue(t, watcher, "value: final\n")
}

func TestFileWatcherObservesSymlinkTargetWritesAndRetargeting(t *testing.T) {
	root := t.TempDir()
	targetDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(targetDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(targetDirectory, "config.yaml")
	if err := os.WriteFile(target, []byte("value: initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.yaml")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	watcher := newFileWatcher(t, path)
	if err := os.WriteFile(target, []byte("value: updated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFileValue(t, watcher, "value: updated\n")

	nextTarget := filepath.Join(targetDirectory, "next.yaml")
	if err := os.WriteFile(nextTarget, []byte("value: retargeted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(root, "replacement.yaml")
	if err := os.Symlink(nextTarget, replacement); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	waitFileValue(t, watcher, "value: retargeted\n")
	if err := os.WriteFile(nextTarget, []byte("value: final\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFileValue(t, watcher, "value: final\n")
}

func TestFileSourceLoadLogsSuccessfulPath(t *testing.T) {
	var output bytes.Buffer
	previous := log.GetLogger()
	log.SetLogger(kratoslog.NewStdLogger(&output))
	t.Cleanup(func() { log.SetLogger(previous) })
	filename := filepath.Join(t.TempDir(), "config.yaml")
	const content = "password: private-value"
	if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := newFileSource(filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Load(); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"DEBUG", "function=fileSource.Load", "path=" + filename, "Loaded configuration file"} {
		if !strings.Contains(output.String(), field) {
			t.Fatalf("missing %q in log: %s", field, output.String())
		}
	}
	if strings.Contains(output.String(), "private-value") {
		t.Fatal("configuration content leaked into log")
	}
	output.Reset()
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Load(); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "Loaded configuration file") {
		t.Fatal("failed read logged as successful")
	}
}
