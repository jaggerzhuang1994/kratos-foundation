package file

import (
	"os"
	"path/filepath"
	"testing"

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
	logger, releaseLogger, logErr := log.NewLogger()
	if logErr != nil {
		t.Fatal(logErr)
	}
	t.Cleanup(releaseLogger)
	paths := PathList{first, second}

	sources, err := NewSources(logger, paths)
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
	if value != "two" {
		t.Fatalf("effective value = %q, want second file override", value)
	}
}

func TestNewSourcesHandlesEmptyUnmatchedInvalidAndDuplicatePaths(t *testing.T) {
	logger, releaseLogger, logErr := log.NewLogger()
	if logErr != nil {
		t.Fatal(logErr)
	}
	t.Cleanup(releaseLogger)

	empty, err := NewSources(logger, nil)
	if err != nil || empty != nil {
		t.Fatalf("NewSources(nil) = %#v, %v; want nil, nil", empty, err)
	}
	unmatched, err := NewSources(logger, PathList{filepath.Join(t.TempDir(), "*.yaml")})
	if err != nil || unmatched != nil {
		t.Fatalf("NewSources(unmatched) = %#v, %v; want nil, nil", unmatched, err)
	}
	if _, err := NewSources(logger, PathList{"["}); err == nil {
		t.Fatal("NewSources(invalid glob) error = nil")
	}

	filename := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(filename, []byte("value: one\n"), 0o600); err != nil {
		t.Fatalf("write duplicate config: %v", err)
	}
	deduplicated, err := NewSources(logger, PathList{filename, filename})
	if err != nil {
		t.Fatalf("NewSources(duplicates) error = %v", err)
	}
	if len(deduplicated) != 1 {
		t.Fatalf("NewSources(duplicates) count = %d, want 1", len(deduplicated))
	}
}
