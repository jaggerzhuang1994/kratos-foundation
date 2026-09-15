package kafka

import (
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/twmb/franz-go/pkg/kgo"
)

type loggerAdapter struct {
	log log.Logger
}

// newKafkaLogger 将全部 franz-go 日志交给共享 Logger，由运行时模块配置决定过滤。
func newKafkaLogger(logger log.Logger) kgo.Logger {
	return &loggerAdapter{log: logger}
}

// Level 接受所有级别，避免 SDK 在热更新前永久过滤 Debug 日志。
func (l *loggerAdapter) Level() kgo.LogLevel {
	return kgo.LogLevelDebug
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
