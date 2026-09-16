package main

import (
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"path/filepath"
	"testing"
)

func TestInitializeAndCleanup(t *testing.T) {
	t.Setenv("DISABLE_CONSUL", "true")
	t.Setenv("LOG_FILE_ENABLE", "false")
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
	t.Setenv("LOG_FILE_ENABLE", "false")
	if _, err := newSpec(app.NewSpec(), server.NewSpec(), job.NewSpec(), configPath(t.TempDir())); err == nil {
		t.Fatal("directory accepted as configuration")
	}
}
