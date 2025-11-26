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
	// 日志文件在轮转前允许的最大大小。默认为 100 MB。
	MaxSize int
	// 是根据备份文件名中编码的时间戳来保留旧日志文件的最大天数。
	// 注意：一天被定义为 24 小时，可能与日历中的自然日不完全对应，
	// 比如受到夏令时、闰秒等影响。默认不会因为时间而删除旧日志文件。
	MaxFileAge int
	// 要保留的旧日志文件的最大数量。
	// 默认会保留所有旧日志文件（但 MaxAge 仍可能导致旧文件被删除）。
	MaxFiles int
	// 决定备份文件名中的时间戳是否使用本地时间。默认使用 UTC 时间。
	LocalTime bool
	// 决定轮转后的日志文件是否使用 gzip 压缩。默认不压缩。
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
