package file

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	logger, releaseLogger, logErr := log.NewLogger()
	if logErr != nil {
		t.Fatal(logErr)
	}
	t.Cleanup(releaseLogger)
	sources, err := NewSources(logger, PathList{path})
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

func TestDirectoryWatcherPublishesDeletionAsFullEmptySnapshot(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(path, []byte("value: initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	watcher := newFileWatcher(t, directory)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-nextFileUpdate(watcher):
		if result.err != nil || len(result.values) != 0 {
			t.Fatalf("deleted snapshot=%v err=%v", result.values, result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("directory deletion was not observed")
	}
	if err := os.WriteFile(path, []byte("value: new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFileValue(t, watcher, "value: new\n")
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

func TestDirectoryWatcherBindsReplacementsBeforePublishing(t *testing.T) {
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

	// The temporary file's own rename may be delivered before the replaced
	// target's remove event, so reload must bind every file in the snapshot.
	replacement := filepath.Join(directory, ".replacement.yaml")
	if err := os.WriteFile(replacement, []byte("value: replaced\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	waitFileValue(t, watcher, "value: replaced\n")
	// A published replacement must already be watched. Do not give the SDK a
	// scheduling delay to finish its automatic leaf registration before writing.
	if err := os.WriteFile(path, []byte("value: immediately-updated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFileValue(t, watcher, "value: immediately-updated\n")

	// Replacing the directory must also discard explicitly watched old leaves.
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
