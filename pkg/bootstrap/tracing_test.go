package bootstrap_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"go.opentelemetry.io/otel/trace"
)

func TestBootstrapAddsTraceAndSpanFieldsPreservingGlobalServiceName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	config := testlog.Config{
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
	shared, cleanup, err := testlog.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	foundationlog.RegisterFields(foundationlog.ServiceNameKey, "existing-service")
	got, err := bootstrap.NewTracingBootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if got != (bootstrap.TracingBootstrap{}) {
		t.Fatalf("bootstrap = %#v, want zero value", got)
	}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:  trace.SpanID{17, 18, 19, 20, 21, 22, 23, 24},
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	logger := shared.WithContext(ctx)
	if err := logger.Log(kratoslog.LevelInfo, "event", "traced"); err != nil {
		t.Fatal(err)
	}
	cleanup()

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := string(written)
	for _, field := range []string{
		"trace.id=0102030405060708090a0b0c0d0e0f10",
		"span.id=1112131415161718",
	} {
		if !strings.Contains(line, field) {
			t.Fatalf("log line lacks %q: %s", field, line)
		}
	}
	if !strings.Contains(line, foundationlog.ServiceNameKey+"=existing-service") {
		t.Fatalf("tracing bootstrap changed service.name: %s", line)
	}
}

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
				if _, err := bootstrap.NewAppInfoBootstrap(spec, integrationAppInfo{}); err != nil {
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
				_, err := bootstrap.NewAppInfoBootstrap(spec, integrationAppInfo{})
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
