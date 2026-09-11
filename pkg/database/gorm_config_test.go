package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/types/known/durationpb"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGORMLoggerWriterClassifiesMessagesAndFlattensFormats(t *testing.T) {
	logger, path := newDatabaseFileLogger(t)
	writer := &gormLoggerWriter{logger: logger}
	writer.Printf("line\n[error] %s", "explicit")
	writer.Printf("trace %v %v", "source", errors.New("driver failed"))
	writer.Printf("trace %v %v", "source", "SLOW SQL >= 200ms")
	writer.Printf("plain %s", "message")

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(written)
	for _, fragment := range []string{
		"ERROR ", "WARN ", "INFO ",
		"line [error] explicit",
		"driver failed",
		"SLOW SQL >= 200ms",
		"plain message",
	} {
		if !strings.Contains(content, fragment) {
			t.Errorf("GORM log lacks %q: %s", fragment, content)
		}
	}
	if secondGORMLogArgumentIsError(nil) || secondGORMLogArgumentIsError([]any{"one"}) ||
		!secondGORMLogArgumentIsError([]any{"one", errors.New("two")}) ||
		secondGORMLogArgumentIsError([]any{"one", "two"}) {
		t.Fatal("GORM error argument classification is incorrect")
	}
	if secondGORMLogArgumentIsSlowSQL(nil) || secondGORMLogArgumentIsSlowSQL([]any{"one"}) ||
		!secondGORMLogArgumentIsSlowSQL([]any{"one", "SLOW SQL >= 10ms"}) ||
		secondGORMLogArgumentIsSlowSQL([]any{"one", "ordinary SQL"}) ||
		secondGORMLogArgumentIsSlowSQL([]any{"one", errors.New("slow")}) {
		t.Fatal("GORM slow SQL argument classification is incorrect")
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
		args []any
		want string
	}{
		{nil, ""}, {[]any{42}, ""}, {[]any{"source"}, ""}, {[]any{"repo.go:x"}, ""}, {[]any{"repo.go:0"}, ""},
		{[]any{"/srv/service/repo.go:110"}, "service/repo.go:110"},
		{[]any{`C:\service\repo.go:110`}, "service/repo.go:110"},
		{[]any{"repo.go:110"}, "repo.go:110"},
	} {
		if got := gormLogCaller(tc.args); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.args, got, tc.want)
		}
	}
}
