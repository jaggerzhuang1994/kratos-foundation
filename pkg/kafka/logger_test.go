package kafka

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestKafkaLoggerLevelsAndForwarding(t *testing.T) {
	logger, logPath := newProviderTestLogger(t)
	for _, test := range []struct {
		name   string
		config *config_pb.ModuleLog
		want   kgo.LogLevel
	}{
		{name: "default", want: kgo.LogLevelInfo},
		{name: "debug", config: &config_pb.ModuleLog{Level: stringp("DEBUG")}, want: kgo.LogLevelDebug},
		{name: "warn", config: &config_pb.ModuleLog{Level: stringp("warn")}, want: kgo.LogLevelWarn},
		{name: "error", config: &config_pb.ModuleLog{Level: stringp("error")}, want: kgo.LogLevelError},
		{name: "disabled", config: &config_pb.ModuleLog{Disable: boolp(true)}, want: kgo.LogLevelNone},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := newKafkaLogger(logger, test.config).Level(); got != test.want {
				t.Fatalf("logger level = %v, want %v", got, test.want)
			}
		})
	}

	adapter := newKafkaLogger(logger, &config_pb.ModuleLog{Level: stringp("debug")})
	adapter.Log(kgo.LogLevelDebug, "debug event", "partition", 1)
	_, _, line, _ := runtime.Caller(0)
	adapter.Log(kgo.LogLevelInfo, "info event", "partition", 2)
	adapter.Log(kgo.LogLevelWarn, "warn event", "partition", 3)
	adapter.Log(kgo.LogLevelError, "error event", "partition", 4)
	written, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logs := string(written)
	for _, fragment := range []string{
		"DEBUG ", "INFO ", "WARN ", "ERROR ",
		"msg=debug event", "msg=info event", "msg=warn event", "msg=error event",
		fmt.Sprintf("caller=kafka/logger_test.go:%d", line+1),
		"partition=1", "partition=2", "partition=3", "partition=4",
	} {
		if !strings.Contains(logs, fragment) {
			t.Errorf("Kafka log lacks %q: %s", fragment, logs)
		}
	}
}
