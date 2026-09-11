package main

import (
	"os"
	"testing"
)

func TestRunRejectsInvalidStartup(t *testing.T) {
	t.Setenv("LOG_FILE_DISABLE", "true")
	for _, args := range [][]string{{"-unknown"}, {"-config", t.TempDir() + "/missing.yaml"}} {
		if err := run(args); err == nil {
			t.Fatal("invalid startup succeeded")
		}
	}
}

func TestMainReportsStartupFailure(t *testing.T) {
	previous := os.Args
	os.Args = []string{"minimal-api", "-unknown"}
	defer func() { os.Args = previous }()
	defer func() {
		if recover() == nil {
			t.Error("startup failure was not reported")
		}
	}()
	main()
}
