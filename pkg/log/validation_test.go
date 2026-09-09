package log

import (
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"testing"
)

func TestValidateConfigRejectsOutputAndFilterErrors(t *testing.T) {
	valid := Config{Level: kratoslog.LevelInfo, TimeFormat: "2006-01-02", Std: OutputConfig{Disable: true, Level: kratoslog.LevelInfo}, File: FileConfig{OutputConfig: OutputConfig{Disable: true, Level: kratoslog.LevelInfo}}}
	if err := validateConfig(valid); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.File.Disable = false
	if err := validateConfig(invalid); err == nil {
		t.Fatal("enabled file output without path accepted")
	}
	invalid = valid
	invalid.Level = kratoslog.Level(99)
	if err := validateConfig(invalid); err == nil {
		t.Fatal("invalid level accepted")
	}
}
