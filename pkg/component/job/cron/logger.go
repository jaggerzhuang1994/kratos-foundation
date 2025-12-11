package cron

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/config"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/robfig/cron/v3"
)

type CronLogger cron.Logger

type cronLogger struct {
	*log.Log
}

func NewCronLogger(
	log *log.Log,
	conf *config.Config,
) CronLogger {
	return &cronLogger{
		log.WithModule("cron", conf.GetLog()).WithCallerDepth(5),
	}
}

func (logger *cronLogger) Info(msg string, keysAndValues ...interface{}) {
	// 跳过 run 日志， job logger 中自己打印
	if msg == "run" || msg == "schedule" || msg == "start" {
		return
	}
	if msg == "wake" {
		logger.With(keysAndValues...).Debug(msg)
	} else {
		logger.With(keysAndValues...).Info(msg)
	}
}

func (logger *cronLogger) Error(err error, msg string, keysAndValues ...interface{}) {
	logger.With(keysAndValues...).With("error", err).Error(msg)
}
