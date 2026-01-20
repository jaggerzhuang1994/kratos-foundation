package job

import (
	"time"

	"github.com/robfig/cron/v3"
)

type CronLogger cron.Logger

type cronLogger struct {
	CronLog
}

func NewCronLogger(
	log CronLog,
) CronLogger {
	return &cronLogger{
		log.AddCallerDepth().WithFilterKeys("now"),
	}
}

func (logger *cronLogger) Info(msg string, keysAndValues ...interface{}) {
	// 跳过 run 日志， job logger 中自己打印
	if msg == "run" || msg == "schedule" || msg == "start" || msg == "stop" {
		return
	}
	if msg == "wake" {
		logger.With(replaceKeysAndValues(keysAndValues)...).Debug(msg)
	} else {
		logger.With(replaceKeysAndValues(keysAndValues)...).Info(msg)
	}
}

func (logger *cronLogger) Error(err error, msg string, keysAndValues ...interface{}) {
	logger.With(replaceKeysAndValues(keysAndValues)...).With("error", err).Error(msg)
}

func replaceKeysAndValues(keysAndValues []any) []any {
	for i := 0; i < len(keysAndValues); i += 2 {
		if key, ok := keysAndValues[i].(string); ok && (key == "now" || key == "next") && i+1 < len(keysAndValues) {
			if val, ok2 := keysAndValues[i+1].(time.Time); ok2 {
				keysAndValues[i+1] = val.Format(time.RFC3339)
			}
		}
	}
	return keysAndValues
}
