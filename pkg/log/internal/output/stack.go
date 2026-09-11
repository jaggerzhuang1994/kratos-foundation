package output

import (
	"errors"
	"fmt"
	"os"

	"github.com/go-kratos/kratos/v2/log"
)

type stackLogger struct {
	loggers []log.Logger
}

// NewStack 创建会依次写入全部输出端的 Logger。
func NewStack(loggers ...log.Logger) log.Logger {
	return &stackLogger{loggers: append([]log.Logger(nil), loggers...)}
}

// Log 聚合写入错误；遇到已关闭输出时停止并返回错误，原 Logger 实例不会切换输出或重试。
func (s *stackLogger) Log(level log.Level, keyvals ...any) error {
	var errs []error
	for index, logger := range s.loggers {
		if err := logger.Log(level, keyvals...); err != nil {
			errs = append(errs, fmt.Errorf("log sink %d: %w", index, err))
			if errors.Is(err, os.ErrClosed) {
				break
			}
		}
	}
	return errors.Join(errs...)
}
