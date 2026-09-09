package main

import (
	"flag"
	"fmt"
	"os"
	"testing"
)

func TestMainPrintsVersionAndExitsSuccessfully(t *testing.T) {
	stdout, err := os.CreateTemp(t.TempDir(), "version-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stdout.Close(); err != nil {
			t.Errorf("close captured stdout: %v", err)
		}
	})

	previousArgs, previousFlags, previousStdout := os.Args, flag.CommandLine, os.Stdout
	defer func() {
		os.Args = previousArgs
		flag.CommandLine = previousFlags
		os.Stdout = previousStdout
	}()
	os.Args = []string{os.Args[0], "--version"}
	flag.CommandLine = flag.NewFlagSet("errors-main-helper", flag.ContinueOnError)
	os.Stdout = stdout
	main()
	os.Stdout = previousStdout
	if err := stdout.Sync(); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(stdout.Name())
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("protoc-gen-kratos-foundation-errors-v2 %v\n", Version)
	if string(output) != want {
		t.Fatalf("main --version output = %q, want %q", output, want)
	}
}
