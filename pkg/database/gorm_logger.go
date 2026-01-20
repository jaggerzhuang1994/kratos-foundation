package database

import (
	"strings"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
	"gorm.io/gorm/logger"
)

type GormLogger logger.Interface

type gormLoggerWriter struct {
	log.Log
}

func NewGormLogger(log log.Log, conf Config) GormLogger {
	gormLogger := conf.GetGorm().GetLogger()

	var level = logger.Silent
	switch gormLogger.GetLevel() {
	case config_pb.GormLogger_INFO:
		level = logger.Info
	case config_pb.GormLogger_WARN:
		level = logger.Warn
	case config_pb.GormLogger_ERROR:
		level = logger.Error
	}

	return logger.New(&gormLoggerWriter{
		log.WithModule("gorm", conf.GetLog()).AddCallerDepth(),
	}, logger.Config{
		SlowThreshold:             gormLogger.GetSlowThreshold().AsDuration(),
		Colorful:                  gormLogger.GetColorful(),
		IgnoreRecordNotFoundError: gormLogger.GetIgnoreRecordNotFoundError(),
		ParameterizedQueries:      gormLogger.GetParameterizedQueries(),
		LogLevel:                  level,
	})
}

func (w *gormLoggerWriter) Printf(s string, i ...any) {
	s = strings.Replace(s, "%s\n", "%s", -1)
	w.Debugf(s, i...)
}
