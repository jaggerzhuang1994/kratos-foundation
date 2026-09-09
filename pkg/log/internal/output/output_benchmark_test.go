package output

import (
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"testing"
)

func BenchmarkFilterLogger(b *testing.B) {
	logger := NewFilter(discardLogger{}, true, map[string]struct{}{
		"password": {},
		"secret.*": {},
	})
	keyvals := []any{
		"request.id", "request-1",
		"password", "hidden",
		"secret.token", "hidden",
		"empty", "",
		"status", 200,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := logger.Log(kratoslog.LevelInfo, keyvals...); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStackLogger(b *testing.B) {
	logger := NewStack(discardLogger{}, discardLogger{})
	keyvals := []any{"message", "benchmark", "request.id", "request-1"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := logger.Log(kratoslog.LevelInfo, keyvals...); err != nil {
			b.Fatal(err)
		}
	}
}
