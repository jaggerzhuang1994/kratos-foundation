package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

const clientMainHelperEnvironment = "KRATOS_FOUNDATION_CLIENT_MAIN_HELPER"

func TestMainPrintsVersionAndExitsSuccessfully(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestClientMainVersionHelperProcess$")
	command.Env = append(os.Environ(), clientMainHelperEnvironment+"=1")
	var stderr bytes.Buffer
	command.Stderr = &stderr

	output, err := command.Output()
	if err != nil {
		t.Fatalf("main --version error = %v, stderr = %q", err, stderr.String())
	}
	want := fmt.Sprintf("protoc-gen-kratos-foundation-client-v2 %v\n", Version)
	if string(output) != want {
		t.Fatalf("main --version output = %q, want %q", output, want)
	}
}

func TestClientMainVersionHelperProcess(_ *testing.T) {
	if os.Getenv(clientMainHelperEnvironment) != "1" {
		return
	}

	os.Args = []string{"protoc-gen-kratos-foundation-client-v2", "--version"}
	flag.CommandLine = flag.NewFlagSet("client-main-helper", flag.ContinueOnError)
	main()
	os.Exit(0)
}
