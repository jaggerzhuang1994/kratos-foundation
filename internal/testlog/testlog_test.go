package testlog

import (
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"testing"
)

func TestNewValidatesAndOwnsLogger(t *testing.T) {
	config := Config{Level: kratoslog.LevelInfo, TimeFormat: "2006-01-02", Std: OutputConfig{Disable: true}, File: FileConfig{OutputConfig: OutputConfig{Disable: true}}}
	logger, cleanup, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := logger.Log(kratoslog.LevelInfo, "msg", "test"); err != nil {
		t.Fatal(err)
	}
	cleanup()
	config.Level = kratoslog.Level(99)
	if _, _, err := New(config); err == nil {
		t.Fatal("invalid config was accepted")
	}
}
