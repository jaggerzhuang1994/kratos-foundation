package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

func TestRunRejectsInvalidStartup(t *testing.T) {
	t.Setenv("LOG_FILE_ENABLE", "false")
	for _, args := range [][]string{{"-unknown"}, {"-config", t.TempDir() + "/missing.yaml"}} {
		if err := run(args); err == nil {
			t.Fatal("invalid startup succeeded")
		}
	}
}

func TestMainReportsStartupFailure(t *testing.T) {
	previous := os.Args
	os.Args = []string{"components-api", "-unknown"}
	defer func() { os.Args = previous }()
	defer func() {
		if recover() == nil {
			t.Error("startup failure was not reported")
		}
	}()
	main()
}

func TestRunLogsCommandBeforeAssemblyFails(t *testing.T) {
	var output bytes.Buffer
	t.Cleanup(log.SetLogger(kratoslog.NewStdLogger(&output)))
	path := t.TempDir() + "/missing.yaml"
	if err := run([]string{"-config", path}); err == nil {
		t.Fatal("missing configuration succeeded")
	}
	for _, field := range []string{"INFO", "module=cmdapp", "event=command.starting", "command=components-api", "version=" + version, "env=", "config_path=" + path} {
		if !strings.Contains(output.String(), field) {
			t.Fatalf("missing %q: %s", field, output.String())
		}
	}
}
