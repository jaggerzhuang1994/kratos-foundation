package output

import (
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

func TestNewFileWritesAndCleanupClosesLogger(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "app.log")
	logger, cleanup, err := NewFile(FileConfig{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.Log(kratoslog.LevelInfo, "message", "written"); err != nil {
		t.Fatal(err)
	}
	cleanup()
	cleanup()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "written") {
		t.Fatalf("file content = %q, want written message", data)
	}
	if err := logger.Log(kratoslog.LevelInfo, "message", "after cleanup"); err == nil {
		t.Fatal("Log() after cleanup returned nil error")
	}
}

func TestNewFileRejectsEmptyPath(t *testing.T) {
	t.Parallel()

	if _, _, err := NewFile(FileConfig{Path: "  "}); err == nil {
		t.Fatal("NewFile() accepted empty path")
	}
}

func TestRotatingFileCleanupFinishesCompressionAndRetention(t *testing.T) {
	for _, compress := range []bool{false, true} {
		name := "plain"
		if compress {
			name = "gzip"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "app.log")
			// 固定历史文件名即可验证数量保留，不依赖墙上时间或轮转间隔。
			for _, name := range []string{
				"app-2000-01-01T00-00-00.000-size.log",
				"app-2000-01-02T00-00-00.000-size.log",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("old backup"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			logger, cleanup, err := NewFile(FileConfig{
				Path: path,
				Rotating: &RotatingFileConfig{
					MaxSize: 1, MaxFiles: 1, Compress: compress,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			payload := strings.Repeat("x", 600*1024)
			if err := logger.Log(kratoslog.LevelInfo, "message", payload); err != nil {
				t.Fatal(err)
			}
			if err := logger.Log(kratoslog.LevelInfo, "message", payload); err != nil {
				t.Fatal(err)
			}
			cleanup()
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 2 {
				t.Fatalf("files after cleanup = %v, want current log and one backup", entries)
			}
			for _, entry := range entries {
				filename := entry.Name()
				if filename == "app.log" {
					continue
				}
				if strings.HasSuffix(filename, ".gz") != compress {
					t.Fatalf("backup = %q, gzip = %v", filename, compress)
				}
				file, err := os.Open(filepath.Join(dir, filename))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := file.Close(); err != nil {
						t.Error(err)
					}
				})
				var reader io.Reader = file
				if compress {
					compressed, err := gzip.NewReader(file)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := compressed.Close(); err != nil {
							t.Error(err)
						}
					})
					reader = compressed
				}
				data, err := io.ReadAll(reader)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(data), payload) {
					t.Fatal("backup does not contain the complete first log entry")
				}
			}
			if err := logger.Log(kratoslog.LevelInfo, "message", "closed"); !errors.Is(err, os.ErrClosed) {
				// StdLogger 直接传回 writer 的关闭错误。
				t.Fatalf("Log after cleanup = %v, want os.ErrClosed", err)
			}
		})
	}
}
