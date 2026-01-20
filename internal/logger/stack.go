package internal_logger

import "github.com/go-kratos/kratos/v2/log"

type stackLogger struct {
	logger []log.Logger
}

func NewStackLogger(logger ...log.Logger) log.Logger {
	return &stackLogger{logger}
}

func (s *stackLogger) Log(level log.Level, keyvals ...interface{}) error {
	for _, logger := range s.logger {
		err := logger.Log(level, keyvals...)
		if err != nil {
			return err
		}
	}
	return nil
}
