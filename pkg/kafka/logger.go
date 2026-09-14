package kafka

import (
	"strings"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/twmb/franz-go/pkg/kgo"
)

type loggerAdapter struct {
	log   log.Logger
	level kgo.LogLevel
}

// newKafkaLogger 把模块日志开关和级别转换为 franz-go 日志适配器。
func newKafkaLogger(logger log.Logger, config *config_pb.ModuleLog) kgo.Logger {
	level := kgo.LogLevelInfo
	if config != nil {
		if config.GetDisable() {
			level = kgo.LogLevelNone
		} else {
			switch strings.ToLower(config.GetLevel()) {
			case "debug":
				level = kgo.LogLevelDebug
			case "warn":
				level = kgo.LogLevelWarn
			case "error":
				level = kgo.LogLevelError
			}
		}
	}
	return &loggerAdapter{log: logger, level: level}
}

// Level 返回适配器接受的最低 franz-go 日志级别。
func (l *loggerAdapter) Level() kgo.LogLevel {
	return l.level
}

// Log 把 franz-go 结构化日志转发到 Foundation Logger。
func (l *loggerAdapter) Log(level kgo.LogLevel, message string, keyvals ...any) {
	logger := l.log.With(keyvals...)
	switch level {
	case kgo.LogLevelError:
		logger.Error(message)
	case kgo.LogLevelWarn:
		logger.Warn(message)
	case kgo.LogLevelInfo:
		logger.Info(message)
	default:
		logger.Debug(message)
	}
}
