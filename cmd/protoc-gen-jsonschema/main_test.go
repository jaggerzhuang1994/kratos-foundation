package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestPluginIdentityAndHelpDescribeSupportedInvocation(t *testing.T) {
	if Version == "" {
		t.Fatal("Version is empty")
	}
	for _, fragment := range []string{"--version", "--help", "--jsonschema_out", "draft-2020-12"} {
		if !strings.Contains(helpMessage, fragment) {
			t.Fatalf("help message lacks %q", fragment)
		}
	}
}

func TestMainPrintsVersionAndHelp(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want string
	}{
		{name: "version", arg: "--version", want: Version + "\n"},
		{name: "help", arg: "--help", want: helpMessage},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := invokeMain(t, test.arg); got != test.want {
				t.Fatalf("main(%q) output = %q, want %q", test.arg, got, test.want)
			}
		})
	}
}

func invokeMain(t *testing.T, arg string) string {
	t.Helper()

	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatalf("create stdout capture: %v", err)
	}
	defer func() { _ = stdout.Close() }()

	originalArgs, originalStdout := os.Args, os.Stdout
	func() {
		os.Args = []string{"protoc-gen-jsonschema", arg}
		os.Stdout = stdout
		defer func() {
			os.Args = originalArgs
			os.Stdout = originalStdout
		}()
		main()
	}()
	if _, err := stdout.Seek(0, 0); err != nil {
		t.Fatalf("rewind stdout capture: %v", err)
	}
	output, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("read stdout capture: %v", err)
	}
	return string(output)
}
