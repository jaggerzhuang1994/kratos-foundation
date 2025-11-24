package logger

import (
	"os"

	"github.com/go-kratos/kratos/v2/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

type FileLoggerConfig struct {
	Level    log.Level
	Path     string
	Rotating *RotatingFileLoggerConfig
}

func NewFileLogger(cfg *FileLoggerConfig) (log.Logger, func(), error) {
	var logger log.Logger
	var err error
	// 禁用文件轮换，则返回文件日志
	if cfg.Rotating == nil {
		var rc func()
		logger, rc, err = newFileLogger(cfg.Path)
		if err != nil {
			return nil, nil, err
		}
		logger = NewFilterLevelLogger(logger, cfg.Level)
		return logger, rc, nil
	}

	logger, rc := newRotatingFileLogger(cfg.Path, cfg.Level.String(), cfg.Rotating)
	return logger, rc, nil
}

func newFileLogger(filename string) (log.Logger, func(), error) {
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil, nil, err
	}
	return log.NewStdLogger(f), func() {
		_ = f.Close()
	}, nil
}

type RotatingFileLoggerConfig struct {
	// 最大文件大小（单位 bytes，默认100MB）
	MaxSize int
	// 最大文件年龄（单位 天，默认永久）
	MaxFileAge int
	// 最大文件数量（默认都保留）
	MaxFiles int
	// 是否使用本地时区拆分文件日志(默认utc)
	LocalTime bool
	// 轮换后的日志文件是否应该使用 gzip 压缩。默认不进行压缩。
	Compress bool
}

func newRotatingFileLogger(filename, level string, config *RotatingFileLoggerConfig) (log.Logger, func()) {
	return newRotatingFileLoggerWithZapEncoder(filename, level, config, zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()))
}

func newRotatingFileLoggerWithZapEncoder(filename, level string, config *RotatingFileLoggerConfig, encoder zapcore.Encoder) (log.Logger, func()) {
	writeSyncer := zapcore.AddSync(&lumberjack.Logger{
		Filename:   filename,
		MaxSize:    config.MaxSize,
		MaxAge:     config.MaxFileAge,
		MaxBackups: config.MaxFiles,
		LocalTime:  config.LocalTime,
		Compress:   config.Compress,
	})

	zapLevel, _ := zapcore.ParseLevel(level)
	core := zapcore.NewCore(encoder, writeSyncer, zapLevel)
	z := zap.New(core)

	return NewZapLogger(z, log.DefaultMessageKey), func() {
		_ = z.Sync()
	}
}
