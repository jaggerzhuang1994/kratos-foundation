package database

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
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
	shared, cleanup, err := foundationlog.NewSharedState(foundationlog.Config{
		Level:      kratoslog.LevelDebug,
		TimeFormat: time.RFC3339,
		Std: foundationlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelDebug,
		},
		File: foundationlog.FileConfig{
			OutputConfig: foundationlog.OutputConfig{Level: kratoslog.LevelDebug},
			Path:         path,
			Rotating:     foundationlog.RotatingConfig{Disable: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return foundationlog.NewLogger(shared), path
}
