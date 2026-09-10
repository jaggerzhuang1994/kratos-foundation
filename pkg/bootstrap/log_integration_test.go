package bootstrap_test

import (
	"context"
	"fmt"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"go.opentelemetry.io/otel/trace"
)

type integrationAppInfo struct{}

func (integrationAppInfo) ID() string                  { return "orders-1" }
func (integrationAppInfo) Name() string                { return "orders" }
func (integrationAppInfo) Version() string             { return "v1.2.3" }
func (integrationAppInfo) Metadata() map[string]string { return nil }

func TestAppInfoAndTracingLogContributionsComposeInEitherOrder(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*app.Spec, foundationlog.Logger) error
	}{
		{
			name: "appinfo then tracing",
			apply: func(spec *app.Spec, shared foundationlog.Logger) error {
				if _, err := bootstrap.NewAppInfoBootstrap(integrationAppInfo{}, spec); err != nil {
					return err
				}
				_, err := bootstrap.NewTracingBootstrap()
				return err
			},
		},
		{
			name: "tracing then appinfo",
			apply: func(spec *app.Spec, shared foundationlog.Logger) error {
				if _, err := bootstrap.NewTracingBootstrap(); err != nil {
					return err
				}
				_, err := bootstrap.NewAppInfoBootstrap(integrationAppInfo{}, spec)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "app.log")
			shared, cleanup, err := testlog.New(integrationLogConfig(path))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			if err := test.apply(app.NewSpec(), shared); err != nil {
				t.Fatal(err)
			}

			spanContext := trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
				SpanID:  trace.SpanID{17, 18, 19, 20, 21, 22, 23, 24},
			})
			ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
			if err := shared.WithContext(ctx).Log(
				kratoslog.LevelInfo,
				"event", "composed",
			); err != nil {
				t.Fatal(err)
			}
			cleanup()

			written, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			line := string(written)
			for _, field := range []string{
				"service.id=orders-1",
				"service.name=orders",
				"service.version=v1.2.3",
				"trace.id=0102030405060708090a0b0c0d0e0f10",
				"span.id=1112131415161718",
			} {
				if !strings.Contains(line, field) {
					t.Fatalf("log line lacks %q: %s", field, line)
				}
			}
		})
	}
}

func integrationLogConfig(path string) testlog.Config {
	return testlog.Config{
		Level:       kratoslog.LevelInfo,
		FilterEmpty: false,
		TimeFormat:  time.RFC3339,
		Std: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: testlog.FileConfig{
			OutputConfig: testlog.OutputConfig{Level: kratoslog.LevelInfo},
			Path:         path,
			Rotating:     testlog.RotatingConfig{Disable: true},
		},
	}
}

func TestGlobalAndInjectedLogCaller(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caller.log")
	shared, release, err := testlog.New(integrationLogConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, cleanup, err := bootstrap.NewLogBootstrap(shared, app.NewSpec())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	_, _, line, _ := runtime.Caller(0)
	kratoslog.Infof("global caller")
	globalLine := line + 1
	logger := shared
	_, _, line, _ = runtime.Caller(0)
	logger.Info("injected caller")
	injectedLine := line + 1
	release()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("unexpected logs: %s", data)
	}
	for i, want := range []int{globalLine, injectedLine} {
		if !strings.Contains(lines[i], fmt.Sprintf("caller=bootstrap/log_integration_test.go:%d", want)) {
			t.Fatalf("caller should point to line %d: %s", want, lines[i])
		}
	}
}
