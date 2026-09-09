package log

import (
	"os"
	"sync"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log/internal/output"
)

// outputLogger 只持有一代 file/std 输出栈。
type outputLogger struct {
	output kratoslog.Logger
	mu     sync.RWMutex
	closed bool
}

// newOutputLogger 组装一组启用的输出端；根级别和根字段过滤由业务 Logger 应用。
func newOutputLogger(config Config) (*outputLogger, func(), error) {
	loggers := make([]kratoslog.Logger, 0, 2)
	releases := make([]func(), 0, 1)

	if !config.File.Disable {
		fileConfig := output.FileConfig{Path: config.File.Path}
		if !config.File.Rotating.Disable {
			fileConfig.Rotating = &output.RotatingFileConfig{
				MaxSize:    config.File.Rotating.MaxSize,
				MaxFileAge: config.File.Rotating.MaxFileAge,
				MaxFiles:   config.File.Rotating.MaxFiles,
				LocalTime:  config.File.Rotating.LocalTime,
				Compress:   config.File.Rotating.Compress,
			}
		}
		fileLogger, release, err := output.NewFile(fileConfig)
		if err != nil {
			return nil, nil, err
		}
		releases = append(releases, release)
		fileLogger = output.NewFilter(
			fileLogger,
			false,
			output.FilterKeysSet(config.File.FilterKeys),
		)
		fileLogger = output.NewLevelFilter(fileLogger, config.File.Level)
		loggers = append(loggers, fileLogger)
	}

	if !config.Std.Disable {
		stdLogger := output.NewStd()
		stdLogger = output.NewFilter(
			stdLogger,
			false,
			output.FilterKeysSet(config.Std.FilterKeys),
		)
		stdLogger = output.NewLevelFilter(stdLogger, config.Std.Level)
		loggers = append(loggers, stdLogger)
	}

	out := &outputLogger{output: output.NewStack(loggers...)}

	release := func() {
		// 独占一代输出的入场边界，等待正在写入的读锁退出后永久关闭。
		out.mu.Lock()
		defer out.mu.Unlock()
		if out.closed {
			return
		}
		out.closed = true
		for index := len(releases) - 1; index >= 0; index-- {
			releases[index]()
		}
	}
	return out, release, nil
}

// Log 允许同代输出并发写入；释放后的旧引用交给 logger.log 切换到新版 Config 重试。
func (l *outputLogger) Log(level kratoslog.Level, keyvals ...any) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.closed {
		return os.ErrClosed
	}
	return l.output.Log(level, keyvals...)
}
