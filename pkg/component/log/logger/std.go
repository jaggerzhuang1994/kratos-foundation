package logger

import (
	"os"

	"github.com/go-kratos/kratos/v2/log"
)

type stdLogger struct {
	stdout log.Logger
	stderr log.Logger
}

func NewStdLogger() log.Logger {
	return &stdLogger{
		log.NewStdLogger(os.Stdout),
		log.NewStdLogger(os.Stderr),
	}
}

func (l *stdLogger) Log(level log.Level, keyvals ...any) error {
	if level >= log.LevelError {
		return l.stderr.Log(level, keyvals...)
	}
	return l.stdout.Log(level, keyvals...)
}
