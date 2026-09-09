package kafka

import (
	"path/filepath"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

func enumP[T ~int32](value T) *T { return &value }

func int32p(value int32) *int32 { return &value }

func stringp(value string) *string { return &value }

func boolp(value bool) *bool { return &value }

func newProviderTestLogger(t testing.TB) (foundationlog.Logger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kafka.log")
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

type failingKafkaModuleLogger struct {
	foundationlog.Logger
	err error
}

func (l failingKafkaModuleLogger) WithModuleConfig(
	string,
	foundationlog.ModuleConfig,
) (foundationlog.Logger, error) {
	return nil, l.err
}
