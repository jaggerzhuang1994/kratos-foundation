package kafka

import (
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
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
