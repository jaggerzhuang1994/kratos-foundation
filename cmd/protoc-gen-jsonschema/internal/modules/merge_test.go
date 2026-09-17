package modules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pgs "github.com/lyft/protoc-gen-star/v2"
)

func TestModuleMergesJSONAndYAMLInputIntoGeneratedSchema(t *testing.T) {
	tests := []struct {
		name    string
		ext     string
		content string
	}{
		{
			name: "JSON",
			ext:  ".json",
			content: `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$ref": "#/$defs/External",
  "$defs": {"External": {"type": "object"}}
}`,
		},
		{
			name: "YAML",
			ext:  ".yaml",
			content: `$schema: https://json-schema.org/draft/2020-12/schema
$ref: '#/$defs/External'
$defs:
  External:
    type: object
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mergePath := filepath.Join(t.TempDir(), "merge"+test.ext)
			if err := os.WriteFile(mergePath, []byte(test.content), 0o600); err != nil {
				t.Fatalf("write merge schema: %v", err)
			}

			parameter := "draft=Draft202012,output_file_suffix=.schema.json,preserve_proto_field_names=true,respect_protojson_int64=true,merge=" + mergePath
			response := runGenerator(t, contractRequest(t, parameter))
			if response.GetError() != "" {
				t.Fatalf("generator response error: %s", response.GetError())
			}
			if len(response.File) != 1 {
				t.Fatalf("generated files = %d, want 1", len(response.File))
			}

			var document map[string]any
			if err := json.Unmarshal([]byte(response.File[0].GetContent()), &document); err != nil {
				t.Fatalf("decode merged output: %v", err)
			}
			if document["$ref"] != "#/$defs/.Merge_0" {
				t.Errorf("merged root $ref = %#v", document["$ref"])
			}
			definitions, ok := document["$defs"].(map[string]any)
			if !ok {
				t.Fatalf("$defs = %#v", document["$defs"])
			}
			assertDefinition(t, definitions, "External", map[string]any{"type": "object"})
			assertDefinition(t, definitions, ".Merge_0", map[string]any{
				"allOf": []any{
					map[string]any{"$ref": "#/$defs/.contract.v1.Config"},
					map[string]any{
						"$defs": map[string]any{"External": map[string]any{"type": "object"}},
						"allOf": []any{map[string]any{"$ref": "#/$defs/External"}},
					},
				},
			})
		})
	}
}

func TestModuleLoadsEverySupportedMergeDraft(t *testing.T) {
	versions := []string{
		draft04Version,
		draft06Version,
		draft07Version,
		draft201909Version,
		draft202012Version,
	}

	for _, version := range versions {
		t.Run(version, func(t *testing.T) {
			mergePath := filepath.Join(t.TempDir(), "merge.json")
			content := fmt.Sprintf(`{"$schema":%q}`, version)
			if err := os.WriteFile(mergePath, []byte(content), 0o600); err != nil {
				t.Fatalf("write merge schema: %v", err)
			}

			module := NewModule()
			module.InitContext(pgs.Context(panicDebugger{}, pgs.Parameters{"merge": mergePath}, "."))
			if module.mergeSchema == nil || module.mergeSchema.Schema() != version {
				t.Fatalf("loaded merge schema = %#v, want version %q", module.mergeSchema, version)
			}
		})
	}
}

func TestModuleRejectsUnreadableMalformedAndUnsupportedMergeSchemas(t *testing.T) {
	tempDir := t.TempDir()
	malformedPath := filepath.Join(tempDir, "malformed.json")
	if err := os.WriteFile(malformedPath, []byte(`{"$schema":`), 0o600); err != nil {
		t.Fatalf("write malformed schema: %v", err)
	}
	unsupportedPath := filepath.Join(tempDir, "unsupported.json")
	if err := os.WriteFile(unsupportedPath, []byte(`{"$schema":"https://example.com/unknown"}`), 0o600); err != nil {
		t.Fatalf("write unsupported schema: %v", err)
	}

	tests := []struct {
		name       string
		path       string
		wantPrefix string
	}{
		{name: "unreadable", path: filepath.Join(tempDir, "missing.json"), wantPrefix: "failed read merge schema"},
		{name: "malformed", path: malformedPath, wantPrefix: "failed detect merge schema"},
		{name: "unsupported draft", path: unsupportedPath, wantPrefix: "unsupported merge schema https://example.com/unknown"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := captureGeneratorFailure(t, func() {
				module := NewModule()
				module.InitContext(pgs.Context(panicDebugger{}, pgs.Parameters{"merge": test.path}, "."))
			})
			if !strings.Contains(failure, test.wantPrefix) {
				t.Fatalf("failure = %q, want substring %q", failure, test.wantPrefix)
			}
		})
	}
}

type generatorFailure string

type panicDebugger struct{}

func (panicDebugger) Log(...any)               {}
func (panicDebugger) Logf(string, ...any)      {}
func (panicDebugger) Debug(...any)             {}
func (panicDebugger) Debugf(string, ...any)    {}
func (panicDebugger) Push(string) pgs.Debugger { return panicDebugger{} }
func (panicDebugger) Pop() pgs.Debugger        { return panicDebugger{} }
func (panicDebugger) Exit(code int)            { panic(generatorFailure(fmt.Sprintf("exit %d", code))) }
func (panicDebugger) Fail(values ...any)       { panic(generatorFailure(fmt.Sprint(values...))) }
func (panicDebugger) Failf(format string, v ...any) {
	panic(generatorFailure(fmt.Sprintf(format, v...)))
}
func (panicDebugger) CheckErr(err error, values ...any) {
	if err != nil {
		panic(generatorFailure(fmt.Sprintf("%s: %v", fmt.Sprint(values...), err)))
	}
}
func (debugger panicDebugger) Assert(ok bool, values ...any) {
	if !ok {
		debugger.Fail(values...)
	}
}

func captureGeneratorFailure(t *testing.T, run func()) (failure string) {
	t.Helper()

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("generator did not fail")
		}
		value, ok := recovered.(generatorFailure)
		if !ok {
			panic(recovered)
		}
		failure = string(value)
	}()
	run()
	return ""
}

var _ pgs.Debugger = panicDebugger{}

type mergeTransport func(*http.Request) (*http.Response, error)

func (f mergeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type trackedMergeBody struct {
	io.Reader
	closed bool
}

func (b *trackedMergeBody) Close() error { b.closed = true; return nil }

type failedMergeReader struct{}

func (failedMergeReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestRemoteMergeBoundaries(t *testing.T) {
	// 使用传输替身验证真实下载入口，避免依赖网络和真实等待。
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	for _, tc := range []struct {
		name         string
		status       int
		body         io.Reader
		transportErr error
		wantError    string
	}{
		{name: "success", status: 200, body: strings.NewReader(`{"$schema":"ok"}`)},
		{name: "HTTP failure", status: 503, body: strings.NewReader("unavailable"), wantError: "HTTP status 503"},
		{name: "over limit", status: 200, body: bytes.NewReader(bytes.Repeat([]byte{'x'}, (8<<20)+1)), wantError: "exceeds"},
		{name: "read failure", status: 200, body: failedMergeReader{}, wantError: "read merge schema"},
		{name: "transport failure", transportErr: errors.New("connection failed"), wantError: "download merge schema"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackedMergeBody{Reader: tc.body}
			http.DefaultTransport = mergeTransport(func(request *http.Request) (*http.Response, error) {
				deadline, ok := request.Context().Deadline()
				if !ok || time.Until(deadline) > 30*time.Second {
					t.Fatal("download request lacks bounded deadline")
				}
				if tc.transportErr != nil {
					return nil, tc.transportErr
				}
				return &http.Response{StatusCode: tc.status, Body: body, Header: make(http.Header)}, nil
			})
			data, err := readRemoteMerge("https://example.test/merge.json")
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v", err)
				}
			} else if err != nil || string(data) != `{"$schema":"ok"}` {
				t.Fatalf("data=%s error=%v", data, err)
			}
			if tc.transportErr == nil && !body.closed {
				t.Fatal("response body not closed")
			}
		})
	}
	// 覆盖模块选择远程输入的分支。
	http.DefaultTransport = mergeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"$schema":"https://json-schema.org/draft/2020-12/schema"}`)), Header: make(http.Header)}, nil
	})
	module := NewModule()
	module.InitContext(pgs.Context(panicDebugger{}, pgs.Parameters{"merge": "https://example.test/merge.json"}, "."))
	if module.mergeSchema == nil {
		t.Fatal("remote schema was not loaded")
	}
}
