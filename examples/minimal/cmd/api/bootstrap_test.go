package main

import (
	"path/filepath"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

func TestInitializeAndCleanup(t *testing.T) {
	t.Setenv("LOG_FILE_DISABLE", "true")
	path, err := filepath.Abs("../../configs/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	application, cleanup, err := initialize(configPath(path), "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if application == nil {
		t.Fatal("application is nil")
	}
}

func TestSourcesRejectsDirectory(t *testing.T) {
	t.Setenv("LOG_FILE_DISABLE", "true")
	logger, cleanup, err := log.NewLogger()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := newSources(logger, configPath(t.TempDir())); err == nil {
		t.Fatal("directory accepted as configuration")
	}
}
