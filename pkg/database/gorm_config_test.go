package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	kratostracing "github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestGORMLoggerWritesStructuredQueries(t *testing.T) {
	base, path := newDatabaseFileLogger(t)
	g := newGORMLogger(base, &config_pb.GormLogger{
		Level:         config_pb.GormLogger_INFO.Enum(),
		SlowThreshold: durationpb.New(time.Second),
	})
	ctx := foundationlog.WithKv(context.Background(), "request.id", "request-1")
	g.Trace(
		ctx,
		time.Now().Add(-time.Millisecond),
		func() (string, int64) { return "SELECT * FROM users", 2 },
		nil,
	)

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(written)
	for _, fragment := range []string{
		"INFO ",
		"event=gorm.query",
		"duration=",
		"rows=2",
		"sql=SELECT * FROM users",
		"request.id=request-1",
	} {
		if !strings.Contains(content, fragment) {
			t.Errorf("GORM log lacks %q: %s", fragment, content)
		}
	}
	if strings.Contains(content, "[rows:2]") || strings.Contains(content, "msg=") {
		t.Fatalf("GORM log still contains legacy text formatting: %s", content)
	}
}

func TestGORMLoggerCorrelatesQueriesWithActiveSpan(t *testing.T) {
	base, path := newDatabaseFileLogger(t)
	base = base.With(
		foundationlog.TraceIDKey, kratostracing.TraceID(),
		foundationlog.SpanIDKey, kratostracing.SpanID(),
	)
	g := newGORMLogger(base, &config_pb.GormLogger{Level: config_pb.GormLogger_INFO.Enum()})
	provider := tracesdk.NewTracerProvider()
	ctx, span := provider.Tracer("gorm-log-test").Start(context.Background(), "request")
	spanContext := span.SpanContext()
	defer span.End()
	g.Trace(ctx, time.Now(), func() (string, int64) { return "SELECT 1", 1 }, nil)

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"trace.id=" + spanContext.TraceID().String(),
		"span.id=" + spanContext.SpanID().String(),
	} {
		if !strings.Contains(string(written), fragment) {
			t.Errorf("GORM query log lacks %q: %s", fragment, written)
		}
	}
}

func TestGORMLoggerDoesNotInventTraceForBackgroundQuery(t *testing.T) {
	base, path := newDatabaseFileLogger(t)
	base = base.With(
		foundationlog.TraceIDKey, kratostracing.TraceID(),
		foundationlog.SpanIDKey, kratostracing.SpanID(),
	)
	g := newGORMLogger(base, &config_pb.GormLogger{Level: config_pb.GormLogger_INFO.Enum()})
	g.Trace(context.Background(), time.Now(), func() (string, int64) { return "SELECT 1", 1 }, nil)

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range strings.Fields(string(written)) {
		if (strings.HasPrefix(field, "trace.id=") && field != "trace.id=") ||
			(strings.HasPrefix(field, "span.id=") && field != "span.id=") {
			t.Fatalf("background query log contains synthetic trace value %q: %s", field, written)
		}
	}
}

func TestGORMLoggerWritesStructuredSlowAndFailedQueries(t *testing.T) {
	for _, test := range []struct {
		name     string
		config   *config_pb.GormLogger
		begin    time.Time
		err      error
		contains []string
	}{
		{
			name: "slow query",
			config: &config_pb.GormLogger{
				Level:         config_pb.GormLogger_WARN.Enum(),
				SlowThreshold: durationpb.New(time.Millisecond),
			},
			begin:    time.Now().Add(-time.Second),
			contains: []string{"WARN ", "event=gorm.query", "slow_threshold=1ms", "rows=3", "sql=SELECT slow"},
		},
		{
			name: "failed query",
			config: &config_pb.GormLogger{
				Level: config_pb.GormLogger_ERROR.Enum(),
			},
			begin:    time.Now(),
			err:      errors.New("driver failed"),
			contains: []string{"ERROR ", "event=gorm.query", "err=driver failed", "rows=3", "sql=SELECT failed"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			base, path := newDatabaseFileLogger(t)
			g := newGORMLogger(base, test.config)
			g.Trace(
				context.Background(),
				test.begin,
				func() (string, int64) { return "SELECT " + strings.Split(test.name, " ")[0], 3 },
				test.err,
			)

			written, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, fragment := range test.contains {
				if !strings.Contains(string(written), fragment) {
					t.Errorf("GORM log lacks %q: %s", fragment, written)
				}
			}
		})
	}
}

func TestGORMLoggerIgnoresConfiguredRecordNotFound(t *testing.T) {
	base, path := newDatabaseFileLogger(t)
	g := newGORMLogger(base, &config_pb.GormLogger{
		Level:                     config_pb.GormLogger_ERROR.Enum(),
		IgnoreRecordNotFoundError: proto.Bool(true),
	})
	g.Trace(
		context.Background(),
		time.Now(),
		func() (string, int64) { return "SELECT missing", 0 },
		gorm.ErrRecordNotFound,
	)

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 0 {
		t.Fatalf("ignored record-not-found log = %s", written)
	}
}

func TestGORMLoggerSupportsLogModeAndMessages(t *testing.T) {
	base, path := newDatabaseFileLogger(t)
	g := newGORMLogger(base, &config_pb.GormLogger{Level: config_pb.GormLogger_SILENT.Enum()})
	active := g.LogMode(gormlogger.Info)
	active.Info(context.Background(), "info %s\n", "message")
	active.Warn(context.Background(), "warn %s", "message")
	active.Error(context.Background(), "error %s", "message")

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"INFO ", "msg=info message",
		"WARN ", "msg=warn message",
		"ERROR ", "msg=error message",
	} {
		if !strings.Contains(string(written), fragment) {
			t.Errorf("GORM message log lacks %q: %s", fragment, written)
		}
	}
	if lines := strings.Count(string(written), "\n"); lines != 3 {
		t.Fatalf("GORM message logs contain embedded newlines: %q", written)
	}
}

func TestGORMLoggerFiltersParameters(t *testing.T) {
	for _, test := range []struct {
		name          string
		parameterized bool
		wantParams    int
	}{
		{name: "retain parameters", parameterized: false, wantParams: 2},
		{name: "hide parameters", parameterized: true, wantParams: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			base, _ := newDatabaseFileLogger(t)
			g := newGORMLogger(base, &config_pb.GormLogger{
				ParameterizedQueries: proto.Bool(test.parameterized),
			})
			filter := g.(gorm.ParamsFilter)
			sql, params := filter.ParamsFilter(context.Background(), "id IN (?, ?)", 1, 2)
			if sql != "id IN (?, ?)" || len(params) != test.wantParams {
				t.Fatalf("ParamsFilter() = (%q, %v), want unchanged SQL and %d params", sql, params, test.wantParams)
			}
		})
	}
}

func TestGORMLogHandlerPreservesDerivedAttributes(t *testing.T) {
	base, path := newDatabaseFileLogger(t)
	handler := &gormLogHandler{logger: base}
	slog.New(handler).
		With("connection", "primary").
		WithGroup("scope").
		InfoContext(context.Background(), "configured", "queue", "jpush")

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"msg=configured",
		"connection=primary",
		"scope.queue=jpush",
	} {
		if !strings.Contains(string(written), fragment) {
			t.Errorf("derived slog log lacks %q: %s", fragment, written)
		}
	}
}

func newDatabaseFileLogger(t testing.TB) (foundationlog.Logger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "database.log")
	shared, cleanup, err := testlog.New(testlog.Config{
		Level:      kratoslog.LevelDebug,
		TimeFormat: time.RFC3339,
		Std: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelDebug,
		},
		File: testlog.FileConfig{
			OutputConfig: testlog.OutputConfig{Level: kratoslog.LevelDebug},
			Path:         path,
			Rotating:     testlog.RotatingConfig{Disable: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return shared, path
}

func TestMergeGORMConfigSlowThresholdReplacesDuration(t *testing.T) {
	for _, tc := range []struct {
		name     string
		base     time.Duration
		override *durationpb.Duration
		want     time.Duration
	}{
		{"inherit", time.Second, nil, time.Second},
		{"replace_seconds", 1500 * time.Millisecond, durationpb.New(2 * time.Second), 2 * time.Second},
		{"replace_nanos", time.Second, durationpb.New(50 * time.Millisecond), 50 * time.Millisecond},
		{"disable", time.Second, durationpb.New(0), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := &config_pb.Gorm{Logger: &config_pb.GormLogger{SlowThreshold: durationpb.New(tc.base)}}
			override := &config_pb.Gorm{Logger: &config_pb.GormLogger{SlowThreshold: tc.override}}
			got := mergeGORMConfig(base, override)
			if got.GetLogger().GetSlowThreshold().AsDuration() != tc.want {
				t.Fatalf("threshold=%v, want %v", got.GetLogger().GetSlowThreshold(), tc.want)
			}
			// 合并产物须独立拥有 Duration，不能修改传入配置快照。
			got.Logger.SlowThreshold.Seconds = 99
			if base.Logger.SlowThreshold.AsDuration() != tc.base {
				t.Fatal("base was mutated")
			}
			if tc.override != nil && tc.override.AsDuration() != tc.want {
				t.Fatal("override was mutated")
			}
		})
	}
}

func TestGORMCallerUsesQuerySource(t *testing.T) {
	l, path := newDatabaseFileLogger(t)
	g := newGORMLogger(l, &config_pb.GormLogger{Level: config_pb.GormLogger_INFO.Enum(), SlowThreshold: durationpb.New(time.Millisecond)})
	_, _, line, _ := runtime.Caller(0)
	g.Trace(context.Background(), time.Now().Add(-time.Second), func() (string, int64) { return "SELECT 1", 1 }, nil)
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("caller=database/gorm_config_test.go:%d", line+1); !strings.Contains(string(written), want) {
		t.Fatalf("want %s in %s", want, written)
	}
}

func TestGORMCallerFromRealQuery(t *testing.T) {
	l, path := newDatabaseFileLogger(t)
	g := newGORMLogger(l, &config_pb.GormLogger{Level: config_pb.GormLogger_INFO.Enum()})
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "caller.db")), &gorm.Config{Logger: g})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Error(err)
		}
	})
	_, _, line, _ := runtime.Caller(0)
	result := db.Exec("SELECT 1")
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("caller=database/gorm_config_test.go:%d", line+1); !strings.Contains(string(written), want) {
		t.Fatalf("want %s in %s", want, written)
	}
}

func TestGORMLogCallerValidatesAndNormalizesSource(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   string
	}{
		{"", ""}, {"source", ""}, {"repo.go:x", ""}, {"repo.go:0", ""},
		{"/srv/service/repo.go:110", "service/repo.go:110"},
		{`C:\service\repo.go:110`, "service/repo.go:110"},
		{"repo.go:110", "repo.go:110"},
	} {
		if got := normalizeGORMLogCaller(tc.source); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.source, got, tc.want)
		}
	}
}
